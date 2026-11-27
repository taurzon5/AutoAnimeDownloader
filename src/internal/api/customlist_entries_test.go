package api

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"AutoAnimeDownloader/src/internal/anilist"
)

// A download custom list that can't be read keeps the account on screen with its main list, marked
// as from the cache, so the orphan refresh stays off (decisions/072, decisions/162).
func TestFetchAniListEntries_UnreadableCustomListMeansUnknownCoverage(t *testing.T) {
	defer anilist.MockAniListDo(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		if strings.Contains(string(body), "MediaListCollection") {
			return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":{"Page":{"pageInfo":{"hasNextPage":false},"mediaList":[]}}}`)), Header: make(http.Header)}, nil
	})()
	list, cached := fetchAniListEntries("customlist-api-user", []string{"CURRENT"}, []string{"RELEASING", "FINISHED"}, []string{"To download"})
	if list == nil || !cached {
		t.Fatalf("list = %v cached = %v, want the main list kept and marked cached", list, cached)
	}
	// Without a download custom list, the same empty account answers an empty, known list.
	if list, _ := fetchAniListEntries("customlist-api-user2", []string{"CURRENT"}, []string{"RELEASING", "FINISHED"}, nil); list == nil {
		t.Fatal("an account without a custom list must answer a known (non-nil) list")
	}
}
