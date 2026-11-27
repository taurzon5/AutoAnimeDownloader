package files

import (
	"path/filepath"
	"testing"

	"AutoAnimeDownloader/src/internal/nyaa"
)

func TestQueueStore_StartsWithTheDefaultQueueAndKeepsTheOrder(t *testing.T) {
	s := NewQueueStore(filepath.Join(t.TempDir(), QueuesFileName))
	list, err := s.All()
	if err != nil || len(list) != 1 || !list[0].IsDefault() || list[0].Name != DefaultQueueName {
		t.Fatalf("fresh store = %+v, %v", list, err)
	}
	p := nyaa.DefaultPriorities()
	_, err = s.Update(func(l []DownloadQueue) ([]DownloadQueue, error) {
		fav := DownloadQueue{ID: "favorites", Name: "Favorites", Priorities: &p}
		return append([]DownloadQueue{fav}, l...), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	list, _ = s.All()
	if len(list) != 2 || list[0].ID != "favorites" || !list[1].IsDefault() {
		t.Errorf("order = %+v, want favorites first", list)
	}

	// The default queue can't hold a ranking or a list of its own, even when the file says so.
	_, _ = s.Update(func(l []DownloadQueue) ([]DownloadQueue, error) {
		l[1].Priorities = &p
		l[1].Animes = []int{5}
		return l, nil
	})
	list, _ = s.All()
	if list[1].Priorities != nil || list[1].Animes != nil {
		t.Errorf("default = %+v", list[1])
	}
}

func TestQueueOfAndMoveAnime(t *testing.T) {
	list := []DownloadQueue{{ID: "a", Name: "A", Animes: []int{1}}, {ID: DefaultQueueID}, {ID: "b", Name: "B"}}
	if pos, q := QueueOf(list, 1); pos != 0 || q.ID != "a" {
		t.Errorf("1 is in %d %s", pos, q.ID)
	}
	if pos, q := QueueOf(list, 2); pos != 1 || !q.IsDefault() {
		t.Errorf("2 is in %d %s, want the default", pos, q.ID)
	}
	list = MoveAnime(list, 1, "b")
	if _, q := QueueOf(list, 1); q.ID != "b" || len(list[0].Animes) != 0 {
		t.Errorf("after the move: %+v", list)
	}
	list = MoveAnime(list, 1, DefaultQueueID)
	if _, q := QueueOf(list, 1); !q.IsDefault() || list[1].Animes != nil {
		t.Errorf("back to the default: %+v", list)
	}
}

func TestQueueID(t *testing.T) {
	taken := map[string]bool{"favorites": true}
	has := func(id string) bool { return taken[id] }
	for name, want := range map[string]string{
		"Favorites":       "favorites-2",
		"Old & Long!!":    "old-long",
		"  ":              "queue",
		"Default":         "queue",
		"Fall 2026 Shows": "fall-2026-shows",
	} {
		if got := QueueID(name, has); got != want {
			t.Errorf("QueueID(%q) = %q, want %q", name, got, want)
		}
	}
}
