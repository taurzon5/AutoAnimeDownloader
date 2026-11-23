package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"AutoAnimeDownloader/src/internal/api"
)

func TestTorrentsPrioritizeRoutesByHashCount(t *testing.T) {
	var method, path, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.SuccessResponse{Success: true, Data: nil})
	}))
	defer srv.Close()

	if _, _, err := runCLI(t, "--endpoint", srv.URL, "torrents", "prioritize", "abc"); err != nil {
		t.Fatalf("one hash: %v", err)
	}
	if method != http.MethodPost || path != "/api/v1/torrents/abc/prioritize" {
		t.Errorf("one hash: %s %s", method, path)
	}

	if _, _, err := runCLI(t, "--endpoint", srv.URL, "torrents", "prioritize", "aaa", "bbb"); err != nil {
		t.Fatalf("two hashes: %v", err)
	}
	if method != http.MethodPost || path != "/api/v1/torrents/prioritize" {
		t.Errorf("two hashes: %s %s", method, path)
	}
	var req api.PrioritizeRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("body: %v (%s)", err, body)
	}
	if len(req.Hashes) != 2 || req.Hashes[0] != "aaa" || req.Hashes[1] != "bbb" {
		t.Errorf("hashes = %v", req.Hashes)
	}
}

// Without hashes the command asks for every torrent with missing files (no body: an empty list is
// a 400); with hashes, only those. Either way it prints what the daemon did.
func TestTorrentsRedownloadMissing(t *testing.T) {
	var method, path, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.SuccessResponse{Success: true, Data: map[string]int{"redownloaded": 2, "skipped_no_space": 1}})
	}))
	defer srv.Close()

	out, _, err := runCLI(t, "--endpoint", srv.URL, "torrents", "redownload-missing")
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if method != http.MethodPost || path != "/api/v1/torrents/redownload-missing" || strings.TrimSpace(body) != "" {
		t.Errorf("all: %s %s body %q", method, path, body)
	}
	if !strings.Contains(out, "Redownloading 2 torrent(s); 1 skipped for lack of disk space") {
		t.Errorf("all: output %q", out)
	}

	if _, _, err := runCLI(t, "--endpoint", srv.URL, "torrents", "redownload-missing", "aaa", "bbb"); err != nil {
		t.Fatalf("some: %v", err)
	}
	var req api.RedownloadMissingRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("body: %v (%s)", err, body)
	}
	if len(req.Hashes) != 2 || req.Hashes[0] != "aaa" || req.Hashes[1] != "bbb" {
		t.Errorf("hashes = %v", req.Hashes)
	}
}
