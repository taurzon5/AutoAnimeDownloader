package daemon

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
)

const customListDownloadList = `{"data": {"Page": {"mediaList": [
	{"id": 1, "status": "CURRENT", "progress": 3, "customLists": {}, "media": {
		"id": 100, "format": "TV", "status": "RELEASING", "episodes": 12,
		"title": {"romaji": "Watching"}, "synonyms": [], "relations": {"edges": []}, "airingSchedule": {"nodes": []}
	}}
]}}}`

// The account's collection: "To download" (included) holds a Planning entry and the Watching one,
// "Other" (not included) a Completed one.
const customListCollection = `{"data": {"MediaListCollection": {"hasNextChunk": false, "lists": [
	{"name": "Watching", "isCustomList": false, "entries": [{"id": 1, "mediaId": 100, "status": "CURRENT", "progress": 3}]},
	{"name": "To download", "isCustomList": true, "entries": [
		{"id": 2, "mediaId": 200, "status": "COMPLETED", "progress": 12},
		{"id": 1, "mediaId": 100, "status": "CURRENT", "progress": 3}
	]},
	{"name": "Other", "isCustomList": true, "entries": [{"id": 3, "mediaId": 300, "status": "COMPLETED", "progress": 12}, {"id": 2, "mediaId": 200, "status": "COMPLETED", "progress": 12}]}
]}}}`

const customListMedia200 = `{"data": {"Page": {"media": [
	{"id": 200, "format": "TV", "status": "FINISHED", "episodes": 12,
	 "title": {"romaji": "Planned In List"}, "synonyms": [], "relations": {"edges": []}, "airingSchedule": {"nodes": []}}
]}}}`

func mockCustomListAniList(collectionFails bool) func() {
	return anilist.MockAniListDo(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		reply := customListDownloadList
		switch {
		case strings.Contains(string(body), "MediaListCollection"):
			if collectionFails {
				return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
			}
			reply = customListCollection
		case strings.Contains(string(body), "GetMediaByIDs"):
			reply = customListMedia200
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(reply)), Header: make(http.Header)}, nil
	})
}

func customListConfig() *files.Config {
	return &files.Config{
		AnilistUsernames:      []string{"customlist-user"},
		CompletedAnimePath:    "/tmp/completed",
		DownloadStatuses:      []string{"CURRENT"},
		DownloadMediaStatuses: []string{"RELEASING", "FINISHED"},
		DownloadCustomLists:   []files.CustomListSource{{Account: "anilist:customlist-user", CustomList: "To download"}},
		EpisodeRetryLimit:     5,
	}
}

// A download custom list brings its anime whatever their list status (a Completed one with progress
// 0, to download it whole); a list that isn't included brings nothing, and an entry already in a
// download status isn't added twice.
func TestSearchAnilist_DownloadCustomListBringsAnyStatus(t *testing.T) {
	defer mockCustomListAniList(false)()
	resp, err := searchAnilist(&mockFileManagerForEpisodes{}, customListConfig(), nil)
	if err != nil {
		t.Fatalf("searchAnilist: %v", err)
	}
	var ids []int
	for _, ml := range resp.Data.Page.MediaList {
		ids = append(ids, ml.Media.Id)
		// A Completed entry still downloads from episode 1 (progress 0), and carries every custom list
		// it is in, so an excluded one would see it.
		if ml.Media.Id == 200 && (ml.Status != "COMPLETED" || ml.Progress != 0 || !ml.CustomLists["To download"] || !ml.CustomLists["Other"]) {
			t.Errorf("custom-list entry = status %q progress %d lists %v", ml.Status, ml.Progress, ml.CustomLists)
		}
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []int{100, 200}) {
		t.Fatalf("media ids = %v, want [100 200]", ids)
	}
}

// Being in a download custom list vetoes status deletion; an unreadable list pauses it.
func TestWithoutCustomListed(t *testing.T) {
	defer mockCustomListAniList(false)()
	listed, known := customListedMedia(customListConfig())
	got := withoutCustomListed(map[int]bool{200: true, 400: true}, listed, known)
	if got[200] || !got[400] {
		t.Fatalf("deletable = %v, want only 400", got)
	}
}

func TestWithoutCustomListed_UnreadableListPausesStatusDeletion(t *testing.T) {
	defer mockCustomListAniList(true)()
	listed, known := customListedMedia(customListConfig())
	if got := withoutCustomListed(map[int]bool{400: true}, listed, known); len(got) != 0 {
		t.Fatalf("deletable = %v, want none while the lists can't be read", got)
	}
}

// An account with no download custom list asks nothing more.
func TestCustomListExtras_OnlyForTheAccountsLists(t *testing.T) {
	configs := customListConfig()
	configs.DownloadCustomLists = []files.CustomListSource{{Account: "anilist:someone-else", CustomList: "To download"}}
	if extras, members := customListExtras("customlist-user", configs); extras != nil || members != nil {
		t.Fatalf("extras %v members %v for an account with no list", extras, members)
	}
}

// An anime of a download custom list that is missing from the pass (its media not fetched this time)
// is not "not in watching"; with the lists unreadable, that deletion waits (decisions/162).
func TestHandleSavedEpisodes_CustomListAnimeKeepFiles(t *testing.T) {
	const hash = "custom-list-anime-torrent"
	saved := []files.EpisodeStruct{{EpisodeNumber: 1, AnimeID: 200, EpisodeHash: hash}}
	configs := &files.Config{DeleteWatchedEpisodes: true, AutoDeleteDownloads: true}
	for name, data := range map[string]handleEpisodesData{
		"listed":     {savedEpisodes: saved, customListed: map[int]bool{200: true}},
		"lists gone": {savedEpisodes: saved, customListsGone: true},
	} {
		backend := fakeWithTorrents(hash)
		handleSavedEpisodes(&mockFileManagerForEpisodes{}, configs, backend, testLibrarian(), data)
		if _, ok := backend.Get(hash); !ok {
			t.Errorf("%s: the custom-list anime's torrent was deleted", name)
		}
	}
}

// resolveAnimeDetails asks this, so the debug run counts a Completed member's progress as 0 like the
// pass does (decisions/058, decisions/162).
func TestCompletedCustomListMember(t *testing.T) {
	defer mockCustomListAniList(false)()
	configs := customListConfig()
	if !completedCustomListMember(configs, 200) {
		t.Error("200 is a Completed member of the included list")
	}
	if completedCustomListMember(configs, 300) {
		t.Error("300 is only in a list that isn't included")
	}
	configs.DownloadStatuses = append(configs.DownloadStatuses, "COMPLETED")
	if completedCustomListMember(configs, 200) {
		t.Error("with COMPLETED a download status, the entry's own progress applies")
	}
}
