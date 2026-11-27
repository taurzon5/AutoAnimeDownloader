package anilist

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// franchiseFixture is one node of a fake AniList graph for the franchise walk.
type franchiseFixture struct {
	format   MediaFormat
	episodes int
	year     int
	romaji   string
	kind     string // "" = ANIME
	edges    map[string][]int
}

// mockFranchiseAPI renders two levels of relations and cuts the third, as AniList does
// (decisions/071), so a walk that stopped at the cut would miss the deep entries.
func mockFranchiseAPI(t *testing.T, graph map[int]franchiseFixture) *int {
	t.Helper()
	requests := 0
	var render func(id int, depth int) string
	render = func(id int, depth int) string {
		n := graph[id]
		kind := n.kind
		if kind == "" {
			kind = "ANIME"
		}
		edges := []string{}
		if depth < 2 {
			for rel, ids := range n.edges {
				for _, to := range ids {
					if _, ok := graph[to]; ok {
						edges = append(edges, fmt.Sprintf(`{"relationType":%q,"node":%s}`, rel, render(to, depth+1)))
					}
				}
			}
		}
		return fmt.Sprintf(`{"id":%d,"type":%q,"format":%q,"status":"FINISHED","episodes":%d,"title":{"english":null,"romaji":%q},"synonyms":[],"startDate":{"year":%d},"relations":{"edges":[%s]}}`,
			id, kind, n.format, n.episodes, n.romaji, n.year, strings.Join(edges, ","))
	}
	restore := MockAniListDo(func(req *http.Request) (*http.Response, error) {
		requests++
		body, _ := io.ReadAll(req.Body)
		var parsed struct {
			Variables struct {
				Ids []int `json:"ids"`
			} `json:"variables"`
		}
		_ = json.Unmarshal(body, &parsed)
		out := []string{}
		for _, id := range parsed.Variables.Ids {
			if _, ok := graph[id]; ok {
				out = append(out, render(id, 0))
			}
		}
		return respond(200, `{"data":{"Page":{"media":[`+strings.Join(out, ",")+`]}}}`, nil), nil
	})
	t.Cleanup(restore)
	return &requests
}

func TestGetFranchise_WalksPastTheCutAndNeedsRelationAndName(t *testing.T) {
	graph := map[int]franchiseFixture{
		1: {format: MediaFormatTV, episodes: 26, year: 2019, romaji: "Kimetsu no Yaiba",
			edges: map[string][]int{"SEQUEL": {2}, "ADAPTATION": {90}, "OTHER": {80}}},
		2: {format: MediaFormatMovie, episodes: 1, year: 2020, romaji: "Kimetsu no Yaiba Movie: Mugen Ressha-hen",
			edges: map[string][]int{"PREQUEL": {1}, "SEQUEL": {3}}},
		3: {format: MediaFormatTV, episodes: 11, year: 2021, romaji: "Kimetsu no Yaiba: Yuukaku-hen",
			edges: map[string][]int{"PREQUEL": {2}, "SEQUEL": {4}, "SPIN_OFF": {70}}},
		// Level 3 from the seed: its relations only arrive when the walk reads it again.
		4: {format: MediaFormatTV, episodes: 11, year: 2023, romaji: "Kimetsu no Yaiba: Katanakaji no Sato-hen",
			edges: map[string][]int{"PREQUEL": {3}, "SEQUEL": {5}}},
		5: {format: MediaFormatTV, episodes: 8, year: 2024, romaji: "Kimetsu no Yaiba: Hashira Geiko-hen",
			edges: map[string][]int{"PREQUEL": {4}}},
		// Related, but nothing like the name: a crossover spin-off stays out.
		70: {format: MediaFormatONA, episodes: 3, year: 2022, romaji: "Chuukou Ikkan!! Kimetsu Gakuen Monogatari"},
		// Similar name through OTHER, and the manga through ADAPTATION: never walked.
		80: {format: MediaFormatTV, episodes: 12, year: 2020, romaji: "Kimetsu no Yaiba Parody"},
		90: {format: MediaFormatManga, romaji: "Kimetsu no Yaiba", kind: "MANGA"},
	}
	requests := mockFranchiseAPI(t, graph)

	group, err := GetFranchise(1, PriorityCritical)
	if err != nil {
		t.Fatalf("GetFranchise: %v", err)
	}
	var ids []int
	for _, e := range group.Entries {
		ids = append(ids, e.ID)
	}
	if fmt.Sprint(ids) != "[1 2 3 4 5]" {
		t.Fatalf("entries = %v, want [1 2 3 4 5] (by year)", ids)
	}
	if group.Entries[1].Format != MediaFormatMovie || group.Entries[1].Year != 2020 {
		t.Errorf("movie entry = %+v", group.Entries[1])
	}

	// A day's cache: the second call costs nothing.
	before := *requests
	if _, err := GetFranchise(1, PriorityCritical); err != nil {
		t.Fatal(err)
	}
	if *requests != before {
		t.Errorf("cached group cost %d more requests", *requests-before)
	}
}

func TestSimilarFranchiseTitles(t *testing.T) {
	e := func(romaji, english string) FranchiseEntry { return FranchiseEntry{Romaji: romaji, English: english} }
	cases := []struct {
		a, b FranchiseEntry
		want bool
	}{
		{e("Bleach", ""), e("Bleach: Sennen Kessen-hen", "Bleach: Thousand-Year Blood War"), true},
		{e("Bakemonogatari", ""), e("Nisemonogatari", ""), true},
		{e("Naruto", ""), e("Boruto: Naruto Next Generations", ""), true},
		{e("Shingeki no Kyojin", "Attack on Titan"), e("", "Attack on Titan Final Season"), true},
		{e("Fate/Zero", ""), e("Fate/stay night", ""), false},
		{e("One Piece", ""), e("", ""), false},
	}
	for _, tc := range cases {
		if got := SimilarFranchiseTitles(tc.a, tc.b); got != tc.want {
			t.Errorf("%q ~ %q = %v, want %v", tc.a.Title(), tc.b.Title(), got, tc.want)
		}
	}
}
