package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"AutoAnimeDownloader/src/internal/api"
)

// `priorities set` and `notifications set` send one part of the config to its own endpoint, read
// from a JSON file, as the web pages do.
func TestConfigPartsSetSendTheirPartOnly(t *testing.T) {
	cases := []struct {
		cmd, path, body, want string
	}{
		{"priorities", "/api/v1/config/priorities", `{"codecs":["hevc","h.264"]}`, `"codecs":["hevc","h.264"]`},
		{"notifications", "/api/v1/config/notifications", `{"webhooks":[],"batch_window_seconds":30}`, `"batch_window_seconds":30`},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			var method, path, got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				method, path = r.Method, r.URL.Path
				b, _ := io.ReadAll(r.Body)
				got = string(b)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.SuccessResponse{Success: true, Data: map[string]string{"message": "ok"}})
			}))
			defer srv.Close()
			file := filepath.Join(t.TempDir(), "part.json")
			if err := os.WriteFile(file, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := runCLI(t, "--endpoint", srv.URL, tc.cmd, "set", file); err != nil {
				t.Fatalf("%s set: %v", tc.cmd, err)
			}
			if method != http.MethodPut || path != tc.path || !strings.Contains(got, tc.want) {
				t.Errorf("request = %s %s %s, want PUT %s with %s", method, path, got, tc.path, tc.want)
			}
		})
	}
}

func TestConfigPartsSetRefusesInvalidJSON(t *testing.T) {
	file := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(file, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCLI(t, "--endpoint", "http://127.0.0.1:1", "priorities", "set", file); err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("want an invalid JSON error, got %v", err)
	}
}

// `animes episode-candidates` asks the episode's own endpoint, with the query when given.
func TestAnimesEpisodeCandidatesHitsTheEpisodeEndpoint(t *testing.T) {
	var path, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.SuccessResponse{Success: true, Data: api.TorrentCandidatesResponse{}})
	}))
	defer srv.Close()
	if _, _, err := runCLI(t, "--endpoint", srv.URL, "animes", "episode-candidates", "21", "3", "--query", "Frieren 03"); err != nil {
		t.Fatalf("episode-candidates: %v", err)
	}
	if path != "/api/v1/animes/21/episodes/3/torrent-candidates" || query != "Frieren 03" {
		t.Errorf("request = %s q=%q", path, query)
	}
}
