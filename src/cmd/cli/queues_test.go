package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"AutoAnimeDownloader/src/internal/api"
	"AutoAnimeDownloader/src/internal/nyaa"
)

func TestQueuesCommandsHitTheirEndpoints(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, r.Method+" "+r.URL.Path+" "+strings.TrimSpace(string(body)))
		w.Header().Set("Content-Type", "application/json")
		var data any
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/queues":
			data = []api.Queue{
				{ID: "default", Name: "Default", Default: true, Animes: []int{}},
				{ID: "favorites", Name: "Favorites", Animes: []int{21}, Priorities: nyaa.Priorities{Fansubs: []string{"judas"}}},
			}
		case r.Method == http.MethodPost:
			data = api.Queue{ID: "favorites", Name: "Favorites"}
		case r.Method == http.MethodDelete:
			data = map[string]int{"moved_to_default": 1}
		}
		_ = json.NewEncoder(w).Encode(api.SuccessResponse{Success: true, Data: data})
	}))
	defer srv.Close()

	run := func(args ...string) string {
		t.Helper()
		stdout, _, err := runCLI(t, append([]string{"--endpoint", srv.URL}, args...)...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return stdout
	}
	if out := run("queues"); !strings.Contains(out, "favorites") || !strings.Contains(out, "every other anime") {
		t.Errorf("list:\n%s", out)
	}
	run("queues", "add", "Favorites")
	run("queues", "rename", "favorites", "Top")
	run("queues", "order", "favorites", "default")
	run("queues", "delete", "favorites", "--yes")
	run("animes", "queue", "21", "favorites")
	if out := run("--json", "queues", "priorities", "get", "favorites"); !strings.Contains(out, "judas") {
		t.Errorf("priorities get:\n%s", out)
	}

	want := []string{
		`GET /api/v1/queues `,
		`POST /api/v1/queues {"name":"Favorites"}`,
		`PUT /api/v1/queues/favorites {"name":"Top"}`,
		`PUT /api/v1/queues/order {"ids":["favorites","default"]}`,
		`DELETE /api/v1/queues/favorites `,
		`PUT /api/v1/animes/21/queue {"queue":"favorites"}`,
		`GET /api/v1/queues `,
	}
	if len(calls) != len(want) {
		t.Fatalf("calls = %q", calls)
	}
	for i := range want {
		if strings.TrimSpace(calls[i]) != strings.TrimSpace(want[i]) {
			t.Errorf("call %d = %q, want %q", i, calls[i], want[i])
		}
	}
}

func TestQueuesDeleteNeedsYes(t *testing.T) {
	if _, _, err := runCLI(t, "--endpoint", "http://127.0.0.1:1", "queues", "delete", "favorites"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("err = %v, want the --yes refusal", err)
	}
}
