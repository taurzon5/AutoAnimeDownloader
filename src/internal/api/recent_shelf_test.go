package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"AutoAnimeDownloader/src/internal/daemon"
	"AutoAnimeDownloader/src/internal/files"
)

func TestRecentShelf(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	ago := func(h int) time.Time { return now.Add(-time.Duration(h) * time.Hour) }
	ep := func(n int, hash string, at time.Time) files.EpisodeStruct {
		return files.EpisodeStruct{EpisodeNumber: n, EpisodeHash: hash, DownloadDate: at}
	}
	for _, tc := range []struct {
		name    string
		saved   []files.EpisodeStruct
		watched int
		days    int
		want    *AnimeRecent
	}{
		{"inside the window and unwatched", []files.EpisodeStruct{ep(5, "a", ago(2)), ep(6, "b", ago(1))}, 4, 3,
			&AnimeRecent{Ranges: []EpisodeRange{{5, 6}}, DownloadedAt: ago(1)}},
		{"watched episodes leave the shelf", []files.EpisodeStruct{ep(4, "a", ago(2))}, 4, 3, nil},
		{"older than the window", []files.EpisodeStruct{ep(5, "a", ago(73))}, 0, 3, nil},
		{"the shelf off", []files.EpisodeStruct{ep(5, "a", ago(1))}, 0, 0, nil},
		{"the shelf off, even for a download this instant", []files.EpisodeStruct{ep(5, "a", now)}, 0, 0, nil},
		{"three singles make one run", []files.EpisodeStruct{ep(3, "c", ago(1)), ep(1, "a", ago(3)), ep(2, "b", ago(2))}, 0, 3,
			&AnimeRecent{Ranges: []EpisodeRange{{1, 3}}, DownloadedAt: ago(1)}},
		{"a hole makes two runs", []files.EpisodeStruct{ep(1, "a", ago(1)), ep(2, "b", ago(1)), ep(5, "c", ago(1))}, 0, 3,
			&AnimeRecent{Ranges: []EpisodeRange{{1, 2}, {5, 5}}, DownloadedAt: ago(1)}},
		{"a pack, progress into it shrinks the run", []files.EpisodeStruct{ep(1, "p", ago(5)), ep(2, "p", ago(5)), ep(3, "p", ago(5)), ep(4, "p", ago(5))}, 2, 3,
			&AnimeRecent{Ranges: []EpisodeRange{{3, 4}}, IsBatch: true, DownloadedAt: ago(5)}},
		{"one leftover of a pack is a plain episode", []files.EpisodeStruct{ep(3, "p", ago(5)), ep(4, "p", ago(5))}, 3, 3,
			&AnimeRecent{Ranges: []EpisodeRange{{4, 4}}, DownloadedAt: ago(5)}},
		{"episode 0 can't sit in a range", []files.EpisodeStruct{ep(0, "m", ago(1))}, 0, 3, nil},
		{"exactly at the edge of the window counts", []files.EpisodeStruct{ep(1, "a", ago(72))}, 0, 3,
			&AnimeRecent{Ranges: []EpisodeRange{{1, 1}}, DownloadedAt: ago(72)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := recentShelf(tc.saved, tc.watched, tc.days, now); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// GET /animes carries "recent" on an anime with fresh unwatched downloads.
func TestHandleAnimesCarriesRecent(t *testing.T) {
	defer mockAniListEmpty()()
	fm := &mockFileManager{configs: files.DefaultConfig(), episodes: []files.EpisodeStruct{
		{AnimeID: 7, AnimeName: "Fresh", EpisodeNumber: 1, EpisodeHash: "a", DownloadDate: time.Now().Add(-time.Hour)},
		{AnimeID: 8, AnimeName: "Old", EpisodeNumber: 1, EpisodeHash: "b", DownloadDate: time.Now().Add(-10 * 24 * time.Hour)},
	}}
	w := httptest.NewRecorder()
	handleAnimes(&Server{State: daemon.NewState(), FileManager: fm})(w, httptest.NewRequest(http.MethodGet, "/api/v1/animes", nil))
	var resp struct {
		Data []AnimeInfo `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	for _, a := range resp.Data {
		switch a.AnimeID {
		case 7:
			if a.Recent == nil || !reflect.DeepEqual(a.Recent.Ranges, []EpisodeRange{{1, 1}}) {
				t.Errorf("fresh anime: recent %+v", a.Recent)
			}
		case 8:
			if a.Recent != nil {
				t.Errorf("old anime: recent %+v", a.Recent)
			}
		}
	}
}

// recent_shelf_days: 0..30 accepted, anything else refused (the default is in the files tests).
func TestRecentShelfDaysConfig(t *testing.T) {
	srv := &Server{State: daemon.NewState(), FileManager: &mockFileManager{}}
	for days, want := range map[int]int{0: http.StatusOK, 3: http.StatusOK, 30: http.StatusOK, 31: http.StatusBadRequest, -1: http.StatusBadRequest} {
		config := files.Config{AnilistUsernames: []string{"u"}, CompletedAnimePath: "/tmp/x", CheckInterval: 15, MaxEpisodesPerAnime: 20, EpisodeRetryLimit: 3, RecentShelfDays: days}
		data, _ := json.Marshal(config)
		w := httptest.NewRecorder()
		handleUpdateConfig(srv)(w, httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewBuffer(data)))
		if w.Code != want {
			t.Errorf("recent_shelf_days %d: %d, want %d (%s)", days, w.Code, want, w.Body.String())
		}
	}
}
