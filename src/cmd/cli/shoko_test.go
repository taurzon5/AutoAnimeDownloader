package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"AutoAnimeDownloader/src/internal/api"
)

// shoko status prints Shoko's import folders and the last import request, so the drop folder can
// be set up from the CLI too (decisions/152).
func TestShokoStatusPrintsTheDropFolder(t *testing.T) {
	asked := time.Date(2026, 11, 24, 18, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/shoko/status" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.SuccessResponse{Success: true, Data: api.ShokoStatus{
			Enabled: true, SignedIn: true, URL: "http://shoko:8111",
			LastImportRequest: &asked, ImportError: "shoko Action/ImportNewFiles answered HTTP 403",
			ImportFolders: []api.ShokoImportFolder{
				{ID: 1, Name: "drop", Path: "/import/", DropType: "Source", DropSource: true},
				{ID: 2, Name: "library", Path: "/anime/", DropType: "Destination", Watched: true},
			},
		}})
	}))
	defer srv.Close()

	stdout, _, err := runCLI(t, "--endpoint", srv.URL, "shoko", "status")
	if err != nil {
		t.Fatalf("shoko status: %v", err)
	}
	for _, want := range []string{
		`Shoko drop source 1 "drop": /import/ (Source)`,
		`Shoko import folder 2 "library": /anime/ (Destination, watched)`,
		"Last asked Shoko to import new files",
		"Asking Shoko to import failed (the next pass asks again): shoko Action/ImportNewFiles answered HTTP 403",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
}

// animes settings <id> shoko-packs sends the anime's pack choice; a wrong value never reaches the
// daemon (decisions/153).
func TestAnimesSettingsShokoPacks(t *testing.T) {
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
	for _, value := range []string{"download", "remove", "default"} {
		got = nil
		if _, _, err := runCLI(t, "--endpoint", srv.URL, "animes", "settings", "7", "shoko-packs", value); err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		if got["shoko_pack_episodes"] != value {
			t.Errorf("%s: body = %v", value, got)
		}
	}
	got = nil
	if _, _, err := runCLI(t, "--endpoint", srv.URL, "animes", "settings", "7", "shoko-packs", "leave"); err == nil || got != nil {
		t.Errorf("a wrong value: err=%v body=%v; want an error and no request", err, got)
	}
}
