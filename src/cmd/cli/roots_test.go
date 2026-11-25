package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"AutoAnimeDownloader/src/internal/api"
)

// animes settings <id> root <name|none> sends the anime's download folder; none clears it
// (decisions/154).
func TestAnimesSettingsRoot(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/animes/7/settings" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.SuccessResponse{Success: true})
	}))
	defer srv.Close()
	for arg, want := range map[string]string{"hdd": "hdd", "none": ""} {
		got = nil
		if _, _, err := runCLI(t, "--endpoint", srv.URL, "animes", "settings", "7", "root", arg); err != nil {
			t.Fatalf("%s: %v", arg, err)
		}
		if v, ok := got["root"]; !ok || v != want {
			t.Errorf("%s: body = %v, want root %q", arg, got, want)
		}
	}
}
