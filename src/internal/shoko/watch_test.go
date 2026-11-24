package shoko

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeHub is Shoko's SignalR aggregate hub as measured on v5.3.3: negotiate, then a WebSocket that
// answers the JSON handshake with {}, says OnConnected, and sends the events given, then closes
// with a type 7 message.
func fakeHub(t *testing.T, key string, events []string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("apikey") != key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/signalr/aggregate/negotiate":
			if r.URL.Query().Get("feeds") != "shoko" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			io.WriteString(w, `{"negotiateVersion":1,"connectionId":"c","connectionToken":"tok","availableTransports":[]}`)
		case "/signalr/aggregate":
			if r.URL.Query().Get("id") != "tok" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			ctx := r.Context()
			if _, hs, err := c.Read(ctx); err != nil || !strings.HasPrefix(string(hs), `{"protocol":"json","version":1}`) {
				return
			}
			out := []string{`{}`, `{"type":1,"target":"ShokoEvent:OnConnected","arguments":[null]}`, `{"type":6}`}
			out = append(out, events...)
			out = append(out, `{"type":7,"error":"bye"}`)
			for _, m := range out {
				if err := c.Write(ctx, websocket.MessageText, []byte(m+signalRSeparator)); err != nil {
					return
				}
			}
			time.Sleep(50 * time.Millisecond)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWatchReportsTheSeriesOfFileEvents(t *testing.T) {
	events := []string{
		`{"type":1,"target":"ShokoEvent:FileHashed","arguments":[{"FileID":4,"CrossReferences":[]}]}`,
		`{"type":1,"target":"ShokoEvent:FileMatched","arguments":[{"FileID":4,"CrossReferences":[{"EpisodeID":3,"SeriesID":1},{"EpisodeID":4,"SeriesID":1}]}]}`,
		`{"type":1,"target":"ShokoEvent:FileDeleted","arguments":[{"FileID":2,"CrossReferences":[{"EpisodeID":2,"SeriesID":7}]}]}`,
		`{"type":1,"target":"ShokoEvent:SeriesUpdated","arguments":[{"SeriesID":9}]}`,
	}
	srv := fakeHub(t, "key-1", events)
	c, _ := NewClient(srv.URL, "key-1")
	var mu sync.Mutex
	connected := 0
	var got []Change
	err := c.Watch(context.Background(), func() { mu.Lock(); connected++; mu.Unlock() }, func(ch Change) { mu.Lock(); got = append(got, ch); mu.Unlock() })
	if err == nil || !strings.Contains(err.Error(), "bye") {
		t.Fatalf("Watch = %v, want the hub's close", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if connected != 1 || len(got) != 2 || got[0].Event != "FileMatched" || len(got[0].Series) != 1 || got[0].Series[0] != 1 ||
		got[1].Event != "FileDeleted" || got[1].Series[0] != 7 {
		t.Fatalf("connected %d, changes %+v; want one OnConnected, then series 1 (once) and 7", connected, got)
	}

	bad, _ := NewClient(srv.URL, "wrong")
	if err := bad.Watch(context.Background(), nil, nil); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a wrong key = %v, want ErrUnauthorized", err)
	}
}

func TestParseHubMessage(t *testing.T) {
	if _, connected, err := parseHubMessage(`{}`); !connected || err != nil {
		t.Fatal("the handshake answer is not the connected signal")
	}
	if _, _, err := parseHubMessage(`{"error":"unknown protocol"}`); err == nil {
		t.Fatal("a refused handshake was not an error")
	}
	// An event, not the handshake: not the connected signal.
	if _, connected, _ := parseHubMessage(`{"type":1,"target":"ShokoEvent:OnConnected","arguments":[null]}`); connected {
		t.Fatal("OnConnected was taken as the connected signal")
	}
	if ch, _, _ := parseHubMessage(`{"type":1,"target":"ShokoEvent:FileHashed","arguments":[{"CrossReferences":[{"SeriesID":3}]}]}`); len(ch.Series) != 0 {
		t.Fatalf("FileHashed reported %+v; only matched and deleted files count", ch)
	}
	if _, _, err := parseHubMessage(`{"type":7}`); err == nil {
		t.Fatal("a close was not an error")
	}
	for _, raw := range []string{`{"type":6}`, `not json`} {
		if ch, c, err := parseHubMessage(raw); len(ch.Series) != 0 || c || err != nil {
			t.Fatalf("%s = %+v %v %v, want nothing", raw, ch, c, err)
		}
	}
}

// RefreshSeries re-reads the series an event names: a deletion shows up, which the newer-only
// sync can't see, and a series not known yet is fetched.
func TestStoreRefreshSeries(t *testing.T) {
	f := newFakeShoko(t)
	f.addSeries(Series{ID: 1, MAL: []int{52991}, Name: "Frieren", Episodes: 28}, nil)
	f.addFile(10, 1, 1, t0)
	f.addFile(11, 1, 2, t0)
	c, _ := NewClient(f.server.URL, "key-1")
	store := NewStore(t.TempDir())
	ctx := context.Background()
	tracked := map[int]bool{52991: true, 888: true}

	if err := store.RefreshSeries(ctx, c, f.server.URL, []int{1}, tracked, nil); err != nil || store.OwnedEpisodes(1) != nil {
		t.Fatal("a refresh before the first full sync read something")
	}
	if err := store.Sync(ctx, c, f.server.URL, tracked, nil, false, t0); err != nil {
		t.Fatal(err)
	}
	f.removeFile(11)
	f.addSeries(Series{ID: 3, MAL: []int{888}, Name: "New", Episodes: 1}, nil)
	f.addFile(30, 3, 1, t0.Add(time.Hour))
	if err := store.RefreshSeries(ctx, c, f.server.URL, []int{1, 3}, tracked, nil); err != nil {
		t.Fatal(err)
	}
	if eps := store.OwnedEpisodes(1); len(eps) != 1 || eps[0].Number != 1 {
		t.Fatalf("after a deletion in Shoko: %+v, want only episode 1", eps)
	}
	if eps := store.OwnedEpisodes(3); len(eps) != 1 {
		t.Fatalf("the new series: %+v", eps)
	}
}
