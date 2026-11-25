package anilist

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMALFactsOfAndEpisodesOfMALIDs(t *testing.T) {
	mediaFactsCache.clear()
	malEpisodesCache.clear()
	calls := 0
	defer MockAniListDo(func(req *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(req.Body)
		answer := `{"data":{"Page":{"media":[{"id":154587,"idMal":52991,"episodes":28}]}}}`
		if strings.Contains(string(body), "idMal_in") {
			answer = `{"data":{"Page":{"media":[{"id":1,"idMal":56885,"episodes":10}]}}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(answer)), Header: http.Header{"Content-Type": {"application/json"}}}, nil
	})()

	facts, err := MALFactsOf([]int{154587, 999}, PriorityCritical)
	if err != nil || facts[154587] != (MALFacts{MalID: 52991, Episodes: 28}) {
		t.Fatalf("MALFactsOf = %+v, %v", facts, err)
	}
	if _, ok := facts[999]; ok {
		t.Fatal("a media id AniList didn't answer for has facts")
	}
	counts, err := EpisodesOfMALIDs([]int{56885, 424242}, PriorityCritical)
	if err != nil || counts[56885] != 10 {
		t.Fatalf("EpisodesOfMALIDs = %+v, %v", counts, err)
	}
	if _, ok := counts[424242]; ok {
		t.Fatal("an unknown MAL id has a count")
	}

	// Both cached: no request the second time, the unknown MAL id included.
	before := calls
	if _, err := MALFactsOf([]int{154587}, PriorityCritical); err != nil {
		t.Fatal(err)
	}
	if _, err := EpisodesOfMALIDs([]int{56885, 424242}, PriorityCritical); err != nil {
		t.Fatal(err)
	}
	if calls != before {
		t.Fatalf("cached facts sent %d more requests", calls-before)
	}
}
