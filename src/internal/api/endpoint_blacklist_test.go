package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"AutoAnimeDownloader/src/internal/files"
)

func deleteAnime(t *testing.T, server *Server, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/animes/21"+query, nil)
	req.SetPathValue("id", "21")
	rec := httptest.NewRecorder()
	handleAnimeDelete(server)(rec, req)
	return rec
}

func decodeDelete(t *testing.T, rec *httptest.ResponseRecorder) AnimeDeleteResponse {
	t.Helper()
	var resp struct {
		Data AnimeDeleteResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid response: %v: %s", err, rec.Body.String())
	}
	return resp.Data
}

func episodesOf21() []files.EpisodeStruct {
	return []files.EpisodeStruct{
		{AnimeID: 21, EpisodeNumber: 1, AnimeName: "Standalone Anime"},
		{AnimeID: 21, EpisodeNumber: 2, AnimeName: "Standalone Anime"},
		{AnimeID: 30, EpisodeNumber: 1, AnimeName: "Other"},
	}
}

// TestAnimeDelete_StandaloneGoesWithItsFiles: an anime added by hand is removed, and with
// delete_files its episodes go too — only its own.
func TestAnimeDelete_StandaloneGoesWithItsFiles(t *testing.T) {
	fm := &mockFileManager{configs: standaloneConfig(), standaloneAnimes: []int{21}, episodes: episodesOf21()}
	rec := deleteAnime(t, standaloneWriteServer(fm), "?delete_files=true")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeDelete(t, rec); !got.Removed || got.DeletedEpisodes != 2 || got.Blacklisted {
		t.Fatalf("response = %+v, want removed with 2 episodes deleted", got)
	}
	if len(fm.standaloneAnimes) != 0 {
		t.Errorf("still tracked: %v", fm.standaloneAnimes)
	}
	if len(fm.episodes) != 1 || fm.episodes[0].AnimeID != 30 {
		t.Errorf("episodes left = %+v, want only the other anime's", fm.episodes)
	}
}

// TestAnimeDelete_ListAnimeNeedsAnAction: a list would bring the anime back, so with neither files
// nor blacklist there is nothing the app can do, and it says so instead of pretending.
func TestAnimeDelete_ListAnimeNeedsAnAction(t *testing.T) {
	fm := &mockFileManager{configs: standaloneConfig(), episodes: episodesOf21()}
	rec := deleteAnime(t, standaloneWriteServer(fm), "")
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "ON_A_LIST" {
		t.Fatalf("want 409 ON_A_LIST, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(fm.episodes) != 3 {
		t.Errorf("episodes changed: %+v", fm.episodes)
	}
}

// TestAnimeDelete_ListAnimeFilesAreBlockedAndBlacklisted: a list anime's deleted episodes are
// blocked (the list would download them again), and the blacklist keeps its name for the page.
func TestAnimeDelete_ListAnimeFilesAreBlockedAndBlacklisted(t *testing.T) {
	defer mockStandaloneAniList(emptyListJSON, standaloneMediaJSON)()
	fm := &mockFileManager{configs: standaloneConfig(), episodes: episodesOf21()}
	rec := deleteAnime(t, standaloneWriteServer(fm), "?delete_files=true&blacklist=true")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeDelete(t, rec); got.Removed || got.DeletedEpisodes != 2 || !got.Blacklisted {
		t.Fatalf("response = %+v, want 2 deleted and blacklisted, not removed", got)
	}
	if len(fm.blockedEpisodes) != 2 {
		t.Errorf("blocked = %v, want both deleted episodes", fm.blockedEpisodes)
	}
	b := fm.animeSettings[21].Blacklisted
	if b == nil || b.Name != "Standalone Anime" || b.Cover != "cover.jpg" || b.Format != "TV" {
		t.Fatalf("blacklisting = %+v, want the AniList name, cover and format", b)
	}
}

// TestBlacklistEndpoints: add, list, lift, and lifting what isn't there is a 404.
func TestBlacklistEndpoints(t *testing.T) {
	defer mockStandaloneAniList(emptyListJSON, standaloneMediaJSON)()
	fm := &mockFileManager{configs: standaloneConfig(), episodes: episodesOf21()}
	server := standaloneWriteServer(fm)

	body, _ := json.Marshal(BlacklistAddRequest{MediaID: 21})
	rec := httptest.NewRecorder()
	handleBlacklist(server)(rec, httptest.NewRequest(http.MethodPost, "/api/v1/blacklist", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST: want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handleBlacklist(server)(rec, httptest.NewRequest(http.MethodGet, "/api/v1/blacklist", nil))
	var list struct {
		Data []BlacklistEntry `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 || list.Data[0].AnimeID != 21 || list.Data[0].Name != "Standalone Anime" || list.Data[0].Episodes != 2 {
		t.Fatalf("GET = %+v, want anime 21 with its name and 2 episodes", list.Data)
	}

	lift := func() int {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/blacklist/21", nil)
		req.SetPathValue("id", "21")
		rec := httptest.NewRecorder()
		handleBlacklistRemove(server)(rec, req)
		return rec.Code
	}
	if code := lift(); code != http.StatusOK {
		t.Fatalf("DELETE: want 200, got %d", code)
	}
	if fm.animeSettings[21].Blacklisted != nil {
		t.Fatal("still blacklisted after DELETE")
	}
	if code := lift(); code != http.StatusNotFound {
		t.Fatalf("second DELETE: want 404, got %d", code)
	}
}

// TestStandaloneAdd_BlacklistedAsksFirst: adding a blacklisted anime is refused with BLACKLISTED
// until the request says to take it off the blacklist; then it is taken off and added.
func TestStandaloneAdd_BlacklistedAsksFirst(t *testing.T) {
	defer mockStandaloneAniList(emptyListJSON, standaloneMediaJSON)()
	defer mockEmptyNyaa()()
	fm := &mockFileManager{configs: standaloneConfig(), animeSettings: map[int]files.AnimeSettings{
		21: {Blacklisted: &files.Blacklisting{At: time.Now(), Name: "Standalone Anime"}},
	}}
	server := standaloneWriteServer(fm)

	rec := postStandaloneMagnet(t, server, StandaloneAnimeRequest{MediaID: 21})
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "BLACKLISTED" {
		t.Fatalf("want 409 BLACKLISTED, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(fm.standaloneAnimes) != 0 || fm.animeSettings[21].Blacklisted == nil {
		t.Fatal("a refused add changed something")
	}

	rec = postStandaloneMagnet(t, server, StandaloneAnimeRequest{MediaID: 21, Unblacklist: true})
	if rec.Code != http.StatusOK {
		t.Fatalf("with unblacklist: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if fm.animeSettings[21].Blacklisted != nil {
		t.Error("still blacklisted after adding with unblacklist")
	}
	if len(fm.standaloneAnimes) != 1 || fm.standaloneAnimes[0] != 21 {
		t.Errorf("standalone = %v, want [21]", fm.standaloneAnimes)
	}
}

// TestStandaloneAdd_RefusedAddKeepsBlacklist: the blacklist is lifted only once the anime is added;
// an add refused on the way (here a bad magnet) leaves it blacklisted (decisions/108's rule for the id).
func TestStandaloneAdd_RefusedAddKeepsBlacklist(t *testing.T) {
	defer mockStandaloneAniList(emptyListJSON, standaloneMediaJSON)()
	fm := &mockFileManager{configs: standaloneConfig(), animeSettings: map[int]files.AnimeSettings{
		21: {Blacklisted: &files.Blacklisting{At: time.Now()}},
	}}
	rec := postStandaloneMagnet(t, standaloneWriteServer(fm), StandaloneAnimeRequest{MediaID: 21, Magnet: "not-a-magnet", Unblacklist: true})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if fm.animeSettings[21].Blacklisted == nil {
		t.Fatal("a refused add took the anime off the blacklist")
	}
}
