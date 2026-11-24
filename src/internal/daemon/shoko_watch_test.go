package daemon

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/nyaa"
)

// liveShoko serves Shoko's API for Frieren and its event hub. delete() drops episode 2 and sends
// FileDeleted on the open hub connection.
type liveShoko struct {
	srv      *httptest.Server
	mu       sync.Mutex
	only1    bool
	send     chan string
	dialed   atomic.Int32
	episodes atomic.Int32 // reads of the series' episodes
	lists    atomic.Int32 // reads of every series: full syncs
}

func newLiveShoko(t *testing.T) *liveShoko {
	l := &liveShoko{send: make(chan string, 4)}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("apikey") != "key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v3/Series":
			l.lists.Add(1)
			io.WriteString(w, `{"Total":1,"List":[{"IDs":{"ID":1,"MAL":[52991]},"Name":"Sousou no Frieren","Sizes":{"Total":{"Episodes":28}}}]}`)
		case "/api/v3/File":
			io.WriteString(w, `{"Total":0,"List":[]}`)
		case "/api/v3/Series/1/Episode":
			l.episodes.Add(1)
			ep := func(n int) string {
				return `{"AniDB":{"Type":"Episode","EpisodeNumber":` + string(rune('0'+n)) + `},"Files":[{"ID":` + string(rune('0'+n)) + `,"Locations":[{"RelativePath":"Frieren/0` + string(rune('0'+n)) + `.mkv","IsAccessible":true}]}]}`
			}
			l.mu.Lock()
			list := ep(1) + "," + ep(2)
			if l.only1 {
				list = ep(1)
			}
			l.mu.Unlock()
			io.WriteString(w, `{"Total":2,"List":[`+list+`]}`)
		case "/signalr/aggregate/negotiate":
			io.WriteString(w, `{"connectionToken":"tok"}`)
		case "/signalr/aggregate":
			l.dialed.Add(1)
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			ctx := r.Context()
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
			_ = c.Write(ctx, websocket.MessageText, []byte(`{}`+"\x1e"+`{"type":1,"target":"ShokoEvent:OnConnected","arguments":[null]}`+"\x1e"))
			for {
				select {
				case <-ctx.Done():
					return
				case m := <-l.send:
					if err := c.Write(ctx, websocket.MessageText, []byte(m+"\x1e")); err != nil {
						return
					}
				}
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(l.srv.Close)
	return l
}

func (l *liveShoko) deleteEpisode2() {
	l.mu.Lock()
	l.only1 = true
	l.mu.Unlock()
	l.send <- `{"type":1,"target":"ShokoEvent:FileDeleted","arguments":[{"FileID":2,"CrossReferences":[{"EpisodeID":2,"SeriesID":1}]}]}`
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A file deleted in Shoko reaches the anime page within seconds, without a pass (decisions/148).
func TestShokoWatchRefreshesOnAChange(t *testing.T) {
	t.Cleanup(anilist.MockAniListDo(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"data":{"Page":{"media":[{"id":154587,"idMal":52991,"episodes":28}]}}}`))}, nil
	}))
	l := newLiveShoko(t)
	svc := NewShokoService(t.TempDir())
	svc.settle = 20 * time.Millisecond
	cfg := &files.Config{ShokoEnabled: true, ShokoURL: l.srv.URL, ShokoAPIKey: "key", ShokoRefreshHours: 24, ShokoWatch: true}
	svc.ForPass(context.Background(), cfg, []anilist.MediaList{frierenAnime()}, nil, time.Now())
	if st, _ := svc.AnimeStatus(154587); len(st.Owned) != 2 {
		t.Fatalf("before the change: %+v", st)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { svc.Watch(ctx, func() (*files.Config, error) { return cfg, nil }); close(stopped) }()
	waitFor(t, "the watcher to connect", func() bool { return svc.WatchState().Connected })

	l.deleteEpisode2()
	waitFor(t, "the deletion to reach the anime page", func() bool {
		st, _ := svc.AnimeStatus(154587)
		return len(st.Owned) == 1
	})
	if svc.WatchState().LastEvent.IsZero() {
		t.Fatal("the event was not recorded")
	}

	// A burst (a season imported at once) is one refresh, once the events settle.
	before := l.episodes.Load()
	svc.mu.Lock()
	svc.settle = 300 * time.Millisecond
	svc.mu.Unlock()
	for i := 0; i < 3; i++ {
		l.send <- `{"type":1,"target":"ShokoEvent:FileMatched","arguments":[{"FileID":9,"CrossReferences":[{"EpisodeID":1,"SeriesID":1}]}]}`
	}
	waitFor(t, "the burst's refresh", func() bool { return l.episodes.Load() > before })
	time.Sleep(500 * time.Millisecond)
	if got := l.episodes.Load() - before; got != 1 {
		t.Fatalf("a burst of 3 events read the series %d times, want once", got)
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Watch did not stop with its context")
	}
}

// Off (shoko_watch false, or Shoko off), the watcher doesn't connect.
func TestShokoWatchStaysOffWhenTurnedOff(t *testing.T) {
	l := newLiveShoko(t)
	svc := NewShokoService(t.TempDir())
	cfg := &files.Config{ShokoEnabled: true, ShokoURL: l.srv.URL, ShokoAPIKey: "key", ShokoWatch: false}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	svc.Watch(ctx, func() (*files.Config, error) { return cfg, nil })
	if n := l.dialed.Load(); n != 0 || svc.WatchState().Connected {
		t.Fatalf("connected %d times with shoko_watch off", n)
	}
}

// A deletion made while the watcher was away sends no event it could hear: the catch-up after the
// connection reads the followed series again and sees it.
func TestShokoWatchCatchUpSeesADeletionMadeWhileAway(t *testing.T) {
	t.Cleanup(anilist.MockAniListDo(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"data":{"Page":{"media":[{"id":154587,"idMal":52991,"episodes":28}]}}}`))}, nil
	}))
	l := newLiveShoko(t)
	svc := NewShokoService(t.TempDir())
	cfg := &files.Config{ShokoEnabled: true, ShokoURL: l.srv.URL, ShokoAPIKey: "key", ShokoRefreshHours: 24, ShokoWatch: true}
	svc.ForPass(context.Background(), cfg, []anilist.MediaList{frierenAnime()}, nil, time.Now())
	l.mu.Lock()
	l.only1 = true // deleted while nobody listened
	l.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Watch(ctx, func() (*files.Config, error) { return cfg, nil })
	waitFor(t, "the catch-up to see the deletion", func() bool {
		st, _ := svc.AnimeStatus(154587)
		return len(st.Owned) == 1
	})
}

// Turning shoko_watch off closes a live connection within configCheck, and it stays closed.
func TestShokoWatchClosesWhenTurnedOff(t *testing.T) {
	l := newLiveShoko(t)
	svc := NewShokoService(t.TempDir())
	svc.configCheck = 20 * time.Millisecond
	var mu sync.Mutex
	cfg := files.Config{ShokoEnabled: true, ShokoURL: l.srv.URL, ShokoAPIKey: "key", ShokoWatch: true}
	load := func() (*files.Config, error) { mu.Lock(); defer mu.Unlock(); c := cfg; return &c, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Watch(ctx, load)
	waitFor(t, "the watcher to connect", func() bool { return svc.WatchState().Connected })
	mu.Lock()
	cfg.ShokoWatch = false
	mu.Unlock()
	waitFor(t, "the connection to close", func() bool { return !svc.WatchState().Connected })
	dialed := l.dialed.Load()
	time.Sleep(150 * time.Millisecond)
	if l.dialed.Load() != dialed || svc.WatchState().Connected {
		t.Fatal("the watcher reconnected with shoko_watch off")
	}
}

// Shoko turned off: a late event's re-match must not bring the anime page's matches back.
func TestShokoOffClearsWhatRematchUses(t *testing.T) {
	t.Cleanup(anilist.MockAniListDo(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"data":{"Page":{"media":[{"id":154587,"idMal":52991,"episodes":28}]}}}`))}, nil
	}))
	l := newLiveShoko(t)
	svc := NewShokoService(t.TempDir())
	cfg := &files.Config{ShokoEnabled: true, ShokoURL: l.srv.URL, ShokoAPIKey: "key", ShokoRefreshHours: 24}
	svc.ForPass(context.Background(), cfg, []anilist.MediaList{frierenAnime()}, nil, time.Now())
	cfg.ShokoEnabled = false
	svc.ForPass(context.Background(), cfg, []anilist.MediaList{frierenAnime()}, nil, time.Now())
	svc.rematch()
	if _, ok := svc.AnimeStatus(154587); ok {
		t.Fatal("a re-match after Shoko was turned off brought the matches back")
	}
}

// The queue is a set: a burst of any size is kept whole, and a series named twice is read once.
func TestWatchQueueKeepsEveryEvent(t *testing.T) {
	q := &watchQueue{pending: map[int]bool{}, kick: make(chan struct{}, 1)}
	for i := 1; i <= 1000; i++ {
		q.add([]int{i, i}, false)
	}
	q.add(nil, true)
	ids, catchUp := q.take()
	if len(ids) != 1000 || !catchUp {
		t.Fatalf("took %d series, catch-up %v; want 1000 and true", len(ids), catchUp)
	}
	if ids, catchUp := q.take(); len(ids) != 0 || catchUp {
		t.Fatal("take did not empty the queue")
	}
}

// Every shoko_full_resync_days the pass reads all of Shoko again, for what nothing announced (an
// unlink); the daily refresh in between stays newer-only, and 0 turns the full resync off.
func TestShokoFullResyncEveryNDays(t *testing.T) {
	t.Cleanup(anilist.MockAniListDo(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"data":{"Page":{"media":[{"id":154587,"idMal":52991,"episodes":28}]}}}`))}, nil
	}))
	for _, days := range []int{30, 0} {
		l := newLiveShoko(t)
		svc := NewShokoService(t.TempDir())
		cfg := &files.Config{ShokoEnabled: true, ShokoURL: l.srv.URL, ShokoAPIKey: "key", ShokoRefreshHours: 24, ShokoFullResyncDays: days}
		animes := []anilist.MediaList{frierenAnime()}
		t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
		for _, at := range []time.Time{t0, t0.Add(25 * time.Hour), t0.Add(29 * 24 * time.Hour)} {
			svc.ForPass(context.Background(), cfg, animes, nil, at)
		}
		if n := l.lists.Load(); n != 1 {
			t.Fatalf("days=%d: %d full reads within 29 days, want only the first", days, n)
		}
		svc.ForPass(context.Background(), cfg, animes, nil, t0.Add(30*24*time.Hour))
		want := int32(2)
		if days == 0 {
			want = 1
		}
		if n := l.lists.Load(); n != want {
			t.Fatalf("days=%d: %d full reads after 30 days, want %d", days, n, want)
		}
	}
}

// The debug run mirrors the pass (decisions/058): the episodes in Shoko are marked and not
// searched, read from the last sync.
func TestRunAnimeDebugLeavesOutTheEpisodesInShoko(t *testing.T) {
	list := `{"data": {"Page": {"mediaList": [{"id": 1, "status": "CURRENT", "progress": 0, "media": {
		"id": 21, "episodes": 12, "format": "TV", "status": "RELEASING",
		"title": {"english": "My Anime", "romaji": "Boku no Anime"},
		"synonyms": [], "relations": {"edges": []},
		"coverImage": {"large": "", "medium": ""},
		"airingSchedule": {"nodes": [{"id": 10, "episode": 1, "timeUntilAiring": 0}, {"id": 11, "episode": 2, "timeUntilAiring": 0}, {"id": 12, "episode": 3, "timeUntilAiring": 0}]}
	}}]}}}`
	t.Cleanup(anilist.MockAniListDo(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		answer := list
		if strings.Contains(string(body), "ShokoMALFacts") {
			answer = `{"data":{"Page":{"media":[{"id":21,"idMal":52991,"episodes":12}]}}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(answer)), Header: make(http.Header)}, nil
	}))
	t.Cleanup(nyaa.MockNyaaHttpGet(func(url string) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`<html><body><table class="torrent-list"><tbody></tbody></table></body></html>`)), Header: make(http.Header)}, nil
	}))
	l := newLiveShoko(t) // episodes 1 and 2 in Shoko
	svc := NewShokoService(t.TempDir())
	cfg := &files.Config{MaxEpisodesPerAnime: 12, EpisodeRetryLimit: 3, AnilistUsernames: []string{"user"},
		ShokoEnabled: true, ShokoURL: l.srv.URL, ShokoAPIKey: "key", ShokoRefreshHours: 24}
	eps := 12
	svc.ForPass(context.Background(), cfg, []anilist.MediaList{{Media: anilist.Media{Id: 21, Episodes: &eps}}}, nil, time.Now())

	summary, err := RunAnimeDebug(21, cfg, &debugMockFileManager{}, svc)
	if err != nil {
		t.Fatal(err)
	}
	for _, ep := range summary.Episodes {
		inShoko := ep.Episode <= 2
		if ep.InShoko != inShoko || ep.WouldSearch == inShoko {
			t.Errorf("episode %d: in_shoko %v, would_search %v; want in_shoko %v and searched only when not", ep.Episode, ep.InShoko, ep.WouldSearch, inShoko)
		}
	}
}
