package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"AutoAnimeDownloader/src/internal/daemon"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/nyaa"
	"AutoAnimeDownloader/src/internal/torrents"
)

func TestAnimeReleaseCards(t *testing.T) {
	added := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	saved := []files.EpisodeStruct{
		{AnimeID: 7, EpisodeNumber: 7, EpisodeHash: "pack"},
		{AnimeID: 7, EpisodeNumber: 6, EpisodeHash: "pack"},
		{AnimeID: 7, EpisodeNumber: 1, EpisodeHash: "old"},
		{AnimeID: 7, EpisodeNumber: 2, EpisodeHash: "gone", EpisodeName: "Show - Episode 2"},
		{AnimeID: 7, EpisodeNumber: 3, EpisodeHash: "freed", TorrentReleased: true},
		{AnimeID: 8, EpisodeNumber: 1, EpisodeHash: "other"},
	}
	records := map[string]files.ReleaseRecord{
		"pack":    {Name: "[Erai-raws] Show 01-12 [1080p CR WEB-DL AVC AAC]", Origin: files.ReleaseOriginSearch, AddedAt: added, Seeders: 40, Release: nyaa.ParseRelease("[Erai-raws] Show 01-12 [1080p CR WEB-DL AVC AAC]"), Episodes: map[int][]int{7: {6, 7}}},
		"watched": {Name: "[SubsPlease] Show - 05 (1080p)", Origin: files.ReleaseOriginSearch, AddedAt: added, Episodes: map[int][]int{7: {5}}},
		"other":   {Name: "x", AddedAt: added, Episodes: map[int][]int{8: {1}}},
	}
	live := []torrents.TorrentInfo{
		{Hash: "pack", Name: "client name", Completed: true, Status: "seeding", PiecesHave: 10, PiecesTotal: 10},
		{Hash: "old", Name: "[SubsPlease] Show - 01 (720p) [HASH].mkv", BytesTotal: 300, BytesCompleted: 150, Status: "downloading"},
	}

	cards := animeReleaseCards(7, saved, records, live, true)
	got := map[string]AnimeReleaseCard{}
	var order []string
	for _, c := range cards {
		got[c.Hash] = c
		order = append(order, c.Hash)
	}
	if want := []string{"old", "gone", "freed", "watched", "pack"}; len(order) != len(want) {
		t.Fatalf("cards %v, want %v", order, want)
	} else {
		for i := range want {
			if order[i] != want[i] {
				t.Fatalf("order %v, want %v (by first episode)", order, want)
			}
		}
	}

	if c := got["pack"]; !c.Recorded || c.Name != records["pack"].Name || c.State != ReleaseStateSeeding || c.Progress != 1 ||
		len(c.Episodes) != 2 || c.Episodes[0] != 6 || c.AddedAt == nil || c.Seeders != 40 || c.Release.Group != "erai-raws" {
		t.Errorf("pack card %+v", c)
	}
	if c := got["old"]; c.Recorded || c.Name != live[1].Name || c.State != ReleaseStateDownloading || c.Progress != 0.5 || c.Size != 300 || c.Release.Group != "subsplease" {
		t.Errorf("a torrent from before records were kept: %+v", c)
	}
	// Unrecorded and gone from the client: the episode record's name, and no tag guessed from it.
	if c := got["gone"]; c.State != ReleaseStateMissing || c.Name != "Show - Episode 2" || c.Release.Source != "" || len(c.Release.Assumed) != 0 {
		t.Errorf("gone card %+v", c)
	}
	if c := got["freed"]; c.State != ReleaseStateReleased {
		t.Errorf("released card %+v", c)
	}
	if c := got["watched"]; c.State != ReleaseStateRemoved || len(c.Episodes) != 1 || c.Episodes[0] != 5 || !c.Recorded {
		t.Errorf("removed card %+v", c)
	}
	if _, ok := got["other"]; ok {
		t.Error("another anime's torrent got a card")
	}

	for _, c := range animeReleaseCards(7, saved, records, live, false) {
		if c.State == ReleaseStateRemoved {
			t.Errorf("keep_removed_release_cards off still shows %+v", c)
		}
	}
}

// The endpoint reads the store the daemon writes, and answers an empty list for an anime with
// nothing downloaded.
func TestHandleAnimeReleases(t *testing.T) {
	store := files.NewReleaseStore(filepath.Join(t.TempDir(), "releases.json"))
	daemon.SetReleaseStore(store)
	t.Cleanup(func() { daemon.SetReleaseStore(nil) })
	if err := store.Record("h1", files.ReleaseRecord{Name: "[X] Show - 01 (1080p)", AddedAt: time.Now()}, 7, 1); err != nil {
		t.Fatal(err)
	}
	mock := &mockFileManager{configs: files.DefaultConfig(), episodes: []files.EpisodeStruct{{AnimeID: 7, EpisodeNumber: 1, EpisodeHash: "h1"}}}
	server := &Server{FileManager: mock}
	get := func(id string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/animes/"+id+"/releases", nil)
		req.SetPathValue("id", id)
		handleAnimeReleases(server)(w, req)
		return w
	}
	if w := get("7"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"[X] Show - 01 (1080p)"`) || !strings.Contains(w.Body.String(), `"state":"missing"`) {
		t.Fatalf("GET = %d %s", w.Code, w.Body.String())
	}
	if w := get("9"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatalf("an anime with nothing: %d %s", w.Code, w.Body.String())
	}
	if w := get("x"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad id = %d", w.Code)
	}
}
