package daemon

import (
	"path/filepath"
	"testing"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/nyaa"
)

func withQueueStore(t *testing.T, list []files.DownloadQueue) {
	t.Helper()
	s := files.NewQueueStore(filepath.Join(t.TempDir(), files.QueuesFileName))
	if _, err := s.Update(func([]files.DownloadQueue) ([]files.DownloadQueue, error) { return list, nil }); err != nil {
		t.Fatal(err)
	}
	prev := queueStore.Load()
	SetQueueStore(s)
	t.Cleanup(func() { queueStore.Store(prev) })
}

func mediaList(ids ...int) []anilist.MediaList {
	out := make([]anilist.MediaList, 0, len(ids))
	for _, id := range ids {
		out = append(out, anilist.MediaList{Media: anilist.Media{Id: id}})
	}
	return out
}

// Queues search in their order, the default queue where the user put it; an anime no queue lists
// is in the default one (decisions/164).
func TestQueueGroups_FollowTheQueueOrder(t *testing.T) {
	queues := []files.DownloadQueue{
		{ID: "first", Name: "First", Animes: []int{3}},
		{ID: files.DefaultQueueID},
		{ID: "last", Name: "Last", Animes: []int{1, 4}},
		{ID: "empty", Name: "Empty"},
	}
	groups := queueGroups(mediaList(1, 2, 3, 4, 5), queues)
	var got [][]int
	for _, g := range groups {
		var ids []int
		for _, a := range g {
			ids = append(ids, a.Media.Id)
		}
		got = append(got, ids)
	}
	want := [][]int{{3}, {2, 5}, {1, 4}}
	if len(got) != len(want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("groups = %v, want %v", got, want)
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Fatalf("groups = %v, want %v", got, want)
			}
		}
	}
	if g := queueGroups(mediaList(1, 2), nil); len(g) != 1 || len(g[0]) != 2 {
		t.Errorf("no queues = %v, want one group", g)
	}
}

// A queue's own ranking reorders the results; the default queue keeps the active ranking.
func TestQueueRanking_SortsWithTheQueuesPriorities(t *testing.T) {
	judasFirst := nyaa.DefaultPriorities()
	judasFirst.Fansubs = []string{"judas", "subsplease"}
	withQueueStore(t, []files.DownloadQueue{
		{ID: files.DefaultQueueID},
		{ID: "judas", Name: "Judas", Priorities: &judasFirst, Animes: []int{7}},
	})
	res := "1080p"
	results := []nyaa.TorrentResult{
		{Name: "[SubsPlease] Show - 01 (1080p)", Seeders: "100", Resolution: &res},
		{Name: "[Judas] Show - 01 (1080p)", Seeders: "100", Resolution: &res},
	}
	if got := rankSeriesResults(results, false, queueRanking(7)); got[0].Name != "[Judas] Show - 01 (1080p)" {
		t.Errorf("queue ranking picked %q first", got[0].Name)
	}
	if got := rankSeriesResults(results, false, queueRanking(8)); got[0].Name != results[0].Name {
		t.Errorf("default queue reordered the scrape's ranking: %q first", got[0].Name)
	}
}
