package anilist

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// A failed read is remembered for a minute: the pass asks twice, and the second ask must not send
// the chunks again (decisions/162).
func TestGetCustomListMembers_FailureIsNotRetriedAtOnce(t *testing.T) {
	calls := 0
	defer MockAniListDo(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})()
	if _, err := GetCustomListMembers("fail-user", []string{"To download"}, PriorityCritical); err == nil {
		t.Fatal("want an error")
	}
	first := calls
	if _, err := GetCustomListMembers("fail-user", []string{"To download"}, PriorityCritical); err == nil {
		t.Fatal("want the remembered error")
	}
	if calls != first {
		t.Fatalf("the second read sent %d more requests, want none", calls-first)
	}
}

// A collection with no list at all is AniList degrading the field, not an account without members.
func TestGetCustomListMembers_EmptyCollectionIsAFailure(t *testing.T) {
	defer MockAniListDo(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":{"MediaListCollection":{"hasNextChunk":false,"lists":[]}}}`)), Header: make(http.Header)}, nil
	})()
	if _, err := GetCustomListMembers("empty-user", []string{"To download"}, PriorityCritical); err == nil {
		t.Fatal("an empty collection must read as a failure")
	}
}
