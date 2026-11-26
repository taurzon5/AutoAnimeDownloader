package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"AutoAnimeDownloader/src/internal/api"
)

// TestAnimesRemoveSendsDeleteAndBlacklist: `animes remove` goes to DELETE /animes/{id} with the two
// choices, and says what was done.
func TestAnimesRemoveSendsDeleteAndBlacklist(t *testing.T) {
	var method, path, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, query = r.Method, r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.SuccessResponse{Success: true, Data: api.AnimeDeleteResponse{DeletedEpisodes: 3, Blacklisted: true}})
	}))
	defer srv.Close()

	stdout, _, err := runCLI(t, "--endpoint", srv.URL, "animes", "remove", "21", "--yes", "--delete-episodes", "--blacklist")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if method != http.MethodDelete || path != "/api/v1/animes/21" {
		t.Errorf("request = %s %s, want DELETE /api/v1/animes/21", method, path)
	}
	if !strings.Contains(query, "delete_files=true") || !strings.Contains(query, "blacklist=true") {
		t.Errorf("query = %q, want delete_files and blacklist", query)
	}
	if !strings.Contains(stdout, "3 episode(s) deleted, blacklisted") {
		t.Errorf("stdout = %q", stdout)
	}
}

// TestAnimesAddBlacklistedPointsAtTheFlag: the daemon's BLACKLISTED refusal tells a CLI user which
// flag confirms it.
func TestAnimesAddBlacklistedPointsAtTheFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(api.SuccessResponse{Success: false, Error: &api.ErrorInfo{Code: "BLACKLISTED", Message: "on the blacklist"}})
	}))
	defer srv.Close()

	_, _, err := runCLI(t, "--endpoint", srv.URL, "animes", "add", "21")
	if err == nil || !strings.Contains(err.Error(), "--unblacklist") {
		t.Fatalf("err = %v, want a hint at --unblacklist", err)
	}
}

// TestBlacklistCommands: list, add and remove hit the blacklist endpoints.
func TestBlacklistCommands(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		var data any
		if r.Method == http.MethodGet {
			data = []api.BlacklistEntry{{AnimeID: 21, Name: "Witch", BlacklistedAt: "2026-11-25T22:13:00Z"}}
		}
		_ = json.NewEncoder(w).Encode(api.SuccessResponse{Success: true, Data: data})
	}))
	defer srv.Close()

	stdout, _, err := runCLI(t, "--endpoint", srv.URL, "blacklist")
	if err != nil || !strings.Contains(stdout, "Witch") {
		t.Fatalf("blacklist: %v, stdout %q", err, stdout)
	}
	if _, _, err := runCLI(t, "--endpoint", srv.URL, "blacklist", "add", "21"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, _, err := runCLI(t, "--endpoint", srv.URL, "blacklist", "remove", "21"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	want := []string{"GET /api/v1/blacklist", "POST /api/v1/blacklist", "DELETE /api/v1/blacklist/21"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}
