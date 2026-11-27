package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"AutoAnimeDownloader/src/internal/daemon"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/nyaa"
)

func queueServer(t *testing.T) (*Server, *mockFileManager, *files.QueueStore) {
	t.Helper()
	store := files.NewQueueStore(filepath.Join(t.TempDir(), files.QueuesFileName))
	prev := daemon.QueueStore()
	daemon.SetQueueStore(store)
	t.Cleanup(func() { daemon.SetQueueStore(prev) })
	fm := &mockFileManager{configs: &files.Config{Priorities: nyaa.Priorities{Fansubs: []string{"subsplease"}, Codecs: []string{"h.264"}}}}
	return &Server{State: daemon.NewState(), FileManager: fm}, fm, store
}

func queueCall(t *testing.T, h http.HandlerFunc, method, path, body string, pathValues ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(pathValues); i += 2 {
		req.SetPathValue(pathValues[i], pathValues[i+1])
	}
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

func TestQueues_CreateRenameOrderMoveAndDelete(t *testing.T) {
	server, fm, store := queueServer(t)

	w := queueCall(t, handleQueues(server), http.MethodPost, "/api/v1/queues", `{"name":"Favorites"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct{ Data Queue }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.Data.ID != "favorites" || created.Data.Priorities.Fansubs[0] != "subsplease" {
		t.Fatalf("created = %+v, want a copy of the default ranking", created.Data)
	}
	if w := queueCall(t, handleQueues(server), http.MethodPost, "/api/v1/queues", `{"name":"favorites"}`); w.Code != http.StatusConflict {
		t.Errorf("duplicate name: %d", w.Code)
	}

	if w := queueCall(t, handleQueue(server), http.MethodPut, "/api/v1/queues/favorites", `{"name":"Top"}`, "id", "favorites"); w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	if w := queueCall(t, handleQueueOrder(server), http.MethodPut, "/api/v1/queues/order", `{"ids":["favorites","default"]}`); w.Code != http.StatusOK {
		t.Fatalf("order: %d %s", w.Code, w.Body.String())
	}
	if w := queueCall(t, handleQueueOrder(server), http.MethodPut, "/api/v1/queues/order", `{"ids":["favorites","favorites"]}`); w.Code != http.StatusBadRequest {
		t.Errorf("order naming a queue twice: %d", w.Code)
	}
	if w := queueCall(t, handleAnimeQueue(server), http.MethodPut, "/api/v1/animes/21/queue", `{"queue":"favorites"}`, "id", "21"); w.Code != http.StatusOK {
		t.Fatalf("move: %d %s", w.Code, w.Body.String())
	}
	if w := queueCall(t, handleAnimeQueue(server), http.MethodPut, "/api/v1/animes/21/queue", `{"queue":"nope"}`, "id", "21"); w.Code != http.StatusBadRequest {
		t.Errorf("move to an unknown queue: %d", w.Code)
	}

	// The queue's ranking is merged over its own; the default's goes to config.json.
	if w := queueCall(t, handleQueuePriorities(server), http.MethodPut, "/api/v1/queues/favorites/priorities", `{"fansubs":["judas"]}`, "id", "favorites"); w.Code != http.StatusOK {
		t.Fatalf("queue priorities: %d %s", w.Code, w.Body.String())
	}
	if w := queueCall(t, handleQueuePriorities(server), http.MethodPut, "/api/v1/queues/default/priorities", `{"codecs":["hevc"]}`, "id", "default"); w.Code != http.StatusOK {
		t.Fatalf("default priorities: %d %s", w.Code, w.Body.String())
	}
	list, _ := store.All()
	if list[0].Name != "Top" || list[0].Animes[0] != 21 || list[0].Priorities.Fansubs[0] != "judas" || list[0].Priorities.Codecs[0] != "h.264" || !list[1].IsDefault() {
		t.Fatalf("store = %+v", list)
	}
	if fm.configs.Priorities.Codecs[0] != "hevc" || fm.configs.Priorities.Fansubs[0] != "subsplease" {
		t.Errorf("config priorities = %+v", fm.configs.Priorities)
	}

	if w := queueCall(t, handleQueue(server), http.MethodDelete, "/api/v1/queues/default", "", "id", "default"); w.Code != http.StatusBadRequest {
		t.Errorf("delete default: %d", w.Code)
	}
	w = queueCall(t, handleQueue(server), http.MethodDelete, "/api/v1/queues/favorites", "", "id", "favorites")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"moved_to_default":1`) {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if list, _ := store.All(); len(list) != 1 || !list[0].IsDefault() {
		t.Errorf("after delete: %+v", list)
	}
}

func TestQueues_ListGivesTheDefaultQueueTheConfigRanking(t *testing.T) {
	server, _, _ := queueServer(t)
	w := queueCall(t, handleQueues(server), http.MethodGet, "/api/v1/queues", "")
	var out struct{ Data []Queue }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Data) != 1 {
		t.Fatalf("list: %s", w.Body.String())
	}
	q := out.Data[0]
	if !q.Default || q.Animes == nil || q.Priorities.Fansubs[0] != "subsplease" {
		t.Errorf("default = %+v", q)
	}
}

// A refused add leaves the anime in the queue it was in, as it leaves no id (decisions/108); an add
// that goes through keeps the queue it chose.
func TestStandaloneAdd_QueueOnlyStaysWhenTheAddGoesThrough(t *testing.T) {
	_, _, store := queueServer(t)
	if _, err := store.Update(func(l []files.DownloadQueue) ([]files.DownloadQueue, error) {
		return append(l, files.DownloadQueue{ID: "later", Name: "Later"}), nil
	}); err != nil {
		t.Fatal(err)
	}
	defer mockStandaloneAniList(emptyListJSON, finished24MediaJSON)()
	fm := &mockFileManager{configs: standaloneConfig()}

	rec := postStandaloneMagnet(t, standaloneWriteServer(fm), StandaloneAnimeRequest{MediaID: 21, Magnet: "http://example.com/not-a-magnet", Queue: "later"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
	list, _ := store.All()
	if _, q := files.QueueOf(list, 21); !q.IsDefault() {
		t.Fatalf("refused add left the anime in %q", q.ID)
	}

	rec = postStandaloneMagnet(t, standaloneWriteServer(fm), StandaloneAnimeRequest{MediaID: 21, Queue: "nope"})
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "QUEUE_NOT_FOUND" {
		t.Fatalf("unknown queue: %d %s", rec.Code, rec.Body.String())
	}
}
