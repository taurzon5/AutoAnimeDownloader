package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/torrents"
)

// placedPack is a placed three-file pack in the fake backend, the way a direct-mode add leaves it.
func placedPack(t *testing.T) *torrents.FakeBackend {
	t.Helper()
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = t.TempDir()
	if err := backend.SetPlacement(torrents.PlacementRequest{Hash: hashA, SeriesDir: "Show", Kind: torrents.PlacementPack}); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Add(magnetA); err != nil {
		t.Fatal(err)
	}
	paths := []string{"Pack/Show - 01.mkv", "Pack/Show - 02.mkv", "Pack/Show - 03.mkv"}
	if _, err := backend.PlaceFiles(hashA, paths); err != nil {
		t.Fatal(err)
	}
	var list []torrents.FileInfo
	for _, p := range paths {
		list = append(list, torrents.FileInfo{Path: p, Size: 10, BytesCompleted: int64Ptr(0)})
	}
	backend.SetFiles(hashA, list)
	return backend
}

func qbitFileManager() *mockFileManager {
	return &mockFileManager{configs: &files.Config{CompletedAnimePath: "/tmp/lib", LibraryMode: files.LibraryModeDirect, TorrentClient: files.TorrentClientQBittorrent}}
}

func putFiles(server *Server, hash, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/api/v1/torrents/"+hash+"/files", strings.NewReader(body))
	req.SetPathValue("hash", hash)
	w := httptest.NewRecorder()
	handleTorrentFiles(server)(w, req)
	return w
}

// PUT /torrents/{hash}/files skips a file, and GET then reports it skipped (decisions/165).
func TestHandleTorrentFilesPutSkipsAndGetReportsIt(t *testing.T) {
	backend := placedPack(t)
	server := &Server{Torrents: backend, FileManager: qbitFileManager()}

	w := putFiles(server, hashA, `{"skip":["Pack/Show - 02.mkv"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Skipped int `json:"skipped"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Data.Skipped != 1 {
		t.Fatalf("response = %s, want skipped 1", w.Body.String())
	}

	w = httptest.NewRecorder()
	handleTorrentFiles(server)(w, filesRequest(hashA))
	items := decodeTorrentList(t, w)
	if len(items) != 3 || items[1]["skipped"] != true || items[0]["skipped"] != nil {
		t.Fatalf("files = %v, want only the second skipped", items)
	}
}

// Refusals: the built-in client (409 FILE_SELECTION_UNSUPPORTED), a path the torrent doesn't have
// (400 UNKNOWN_FILE), an empty choice (400), an unknown torrent (404).
func TestHandleTorrentFilesPutRefusals(t *testing.T) {
	backend := placedPack(t)
	cases := []struct {
		name, hash, body string
		fm               *mockFileManager
		code             int
		errCode          string
	}{
		{"built-in client", hashA, `{"skip":["Pack/Show - 02.mkv"]}`, &mockFileManager{configs: &files.Config{LibraryMode: files.LibraryModeDirect}}, http.StatusConflict, "FILE_SELECTION_UNSUPPORTED"},
		{"unknown path", hashA, `{"skip":["Pack/nope.mkv"]}`, qbitFileManager(), http.StatusBadRequest, "UNKNOWN_FILE"},
		{"empty choice", hashA, `{}`, qbitFileManager(), http.StatusBadRequest, "INVALID_BODY"},
		{"unknown torrent", hashB, `{"want":["x"]}`, qbitFileManager(), http.StatusNotFound, "TORRENT_NOT_FOUND"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := putFiles(&Server{Torrents: backend, FileManager: tc.fm}, tc.hash, tc.body)
			if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.errCode) {
				t.Fatalf("got %d %s, want %d %s", w.Code, w.Body.String(), tc.code, tc.errCode)
			}
		})
	}
	if p, _ := backend.Placement(hashA); p.Files[1].Skipped {
		t.Fatal("a refused choice must change nothing")
	}
}

// Replace with skip_files under the built-in client is refused before the anime's episodes are
// deleted: replace removes them first (decisions/165).
func TestHandleReplaceAnimeWithMagnet_FileChoiceRefusedBeforeDeleting(t *testing.T) {
	anilistCalled := false
	defer mockAniListRecorder(&anilistCalled)()
	fm := &mockFileManager{episodes: savedEpisodeFixture()}
	before := len(fm.episodes)
	server, _ := episodeActionServer(t, fm)

	body := `{"magnet":"magnet:?xt=urn:btih:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","skip_files":["Pack/Show - 02.mkv"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/animes/7/replace", strings.NewReader(body))
	req.SetPathValue("id", "7")
	w := httptest.NewRecorder()
	handleReplaceAnimeWithMagnet(server)(w, req)

	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "FILE_SELECTION_UNSUPPORTED") {
		t.Fatalf("got %d %s, want 409 FILE_SELECTION_UNSUPPORTED", w.Code, w.Body.String())
	}
	if len(fm.episodes) != before || anilistCalled {
		t.Fatalf("nothing may be deleted or fetched: %d of %d episodes left, AniList called %v", len(fm.episodes), before, anilistCalled)
	}
}
