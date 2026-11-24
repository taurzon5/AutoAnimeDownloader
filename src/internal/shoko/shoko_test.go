package shoko

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func TestLogin(t *testing.T) {
	f := newFakeShoko(t)
	key, err := Login(context.Background(), f.server.URL, "admin", "pw")
	if err != nil || key != "key-1" {
		t.Fatalf("Login = %q, %v", key, err)
	}
	if _, err := Login(context.Background(), f.server.URL, "admin", "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a wrong password = %v, want ErrUnauthorized", err)
	}
	if _, err := Login(context.Background(), "localhost:8111", "admin", "pw"); err == nil {
		t.Fatal("a URL without a scheme was accepted")
	}
	c, _ := NewClient(f.server.URL, "stale")
	if _, err := c.AllSeries(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a wrong key = %v, want ErrUnauthorized", err)
	}
}

// The query parameters are the measured ones: without includeDataFrom=AniDB an episode has no
// number, type=Normal is ignored (credits come back), an unknown sortOrder is ignored, and only
// include=XRefs names a file's series. The fake answers the same way, so a drifted parameter fails.
func TestClientUsesTheMeasuredParameters(t *testing.T) {
	f := newFakeShoko(t)
	f.addSeries(Series{ID: 1, AniDB: 17617, MAL: []int{52991}, Name: "Frieren", Episodes: 28}, nil)
	f.addFile(10, 1, 1, t0)
	f.addFile(11, 1, 2, t0.Add(time.Hour))
	c, _ := NewClient(f.server.URL, "key-1")
	ctx := context.Background()

	eps, err := c.OwnedEpisodes(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 2 || eps[0].Number != 1 || eps[1].Number != 2 || eps[0].Files[0].ID != 10 || eps[0].Files[0].Path != "Show/ep01.mkv" {
		t.Fatalf("OwnedEpisodes = %+v, want episodes 1 and 2 with their files, no credits", eps)
	}
	var episodeCall string
	for _, r := range f.requestLog() {
		if strings.Contains(r, "/Episode?") {
			episodeCall = r
		}
	}
	if !strings.Contains(episodeCall, "type=Episode") {
		t.Fatalf("episode request %q does not filter on type=Episode", episodeCall)
	}
	// A server that ignores the filter: credits still don't count as episodes.
	f.ignoreType = true
	if eps, err := c.OwnedEpisodes(ctx, 1); err != nil || len(eps) != 2 {
		t.Fatalf("with credits in the answer: %+v, %v; want the 2 regular episodes", eps, err)
	}
	f.ignoreType = false
	files, err := c.FilesImportedSince(ctx, t0.Add(30*time.Minute))
	if err != nil || len(files) != 1 || files[0].ID != 11 || !reflect.DeepEqual(files[0].Series, []int{1}) {
		t.Fatalf("FilesImportedSince = %+v, %v; want only the newer file, with its series", files, err)
	}
	newest, id, err := c.NewestImport(ctx)
	if err != nil || !newest.Equal(t0.Add(time.Hour)) || id != 11 {
		t.Fatalf("NewestImport = %v, %d, %v", newest, id, err)
	}
}

func TestMatchEntry(t *testing.T) {
	series := []Series{
		{ID: 1, MAL: []int{52991, 56885}, Name: "Frieren", Episodes: 28}, // the series and its shorts
		{ID: 2, MAL: []int{100, 101}, Name: "Two cours", Episodes: 24},   // AniDB kept both cours
		{ID: 3, MAL: []int{200}, Name: "Single", Episodes: 12},
		{ID: 4, MAL: []int{300, 301}, Name: "Uneven", Episodes: 30},
		{ID: 5, MAL: []int{400}, Name: "Twice A", Episodes: 12},
		{ID: 6, MAL: []int{400}, Name: "Twice B", Episodes: 12},
	}
	counts := map[int]int{56885: 10, 100: 12, 101: 12, 300: 12, 301: 12}
	malEpisodes := func(id int) (int, bool) { n, ok := counts[id]; return n, ok }
	cases := []struct {
		name  string
		entry Entry
		want  Match
	}{
		{"one MAL id", Entry{MalID: 200, Episodes: 12}, Match{Status: Matched, SeriesID: 3, SeriesName: "Single"}},
		{"one MAL id, count unknown (airing)", Entry{MalID: 200}, Match{Status: Matched, SeriesID: 3, SeriesName: "Single"}},
		{"the series' own count beside its shorts", Entry{MalID: 52991, Episodes: 28}, Match{Status: Matched, SeriesID: 1, SeriesName: "Frieren"}},
		{"the shorts: they don't add up", Entry{MalID: 56885, Episodes: 10}, Match{Status: Partial, SeriesID: 1, SeriesName: "Frieren"}},
		{"second cour at an offset", Entry{MalID: 101, Episodes: 12}, Match{Status: Matched, SeriesID: 2, SeriesName: "Two cours", Offset: 12}},
		{"first cour", Entry{MalID: 100, Episodes: 12}, Match{Status: Matched, SeriesID: 2, SeriesName: "Two cours"}},
		{"cours that don't add up", Entry{MalID: 301, Episodes: 12}, Match{Status: Partial, SeriesID: 4, SeriesName: "Uneven"}},
		{"several MAL ids, count unknown", Entry{MalID: 101}, Match{Status: Partial, SeriesID: 2, SeriesName: "Two cours"}},
		{"two series list it", Entry{MalID: 400, Episodes: 12}, Match{Status: Partial, SeriesID: 5, SeriesName: "Twice A"}},
		{"no series", Entry{MalID: 999, Episodes: 12}, Match{Status: NoMatch}},
		{"no MAL id", Entry{Episodes: 12}, Match{Status: NoMatch}},
	}
	for _, tc := range cases {
		if got := MatchEntry(tc.entry, series, malEpisodes); got != tc.want {
			t.Errorf("%s: MatchEntry = %+v, want %+v", tc.name, got, tc.want)
		}
	}
	// Another listed MAL id with no known count: no offset can be trusted.
	delete(counts, 100)
	if got := MatchEntry(Entry{MalID: 101, Episodes: 12}, series, malEpisodes); got.Status != Partial {
		t.Errorf("an unknown earlier cour = %+v, want Partial", got)
	}
}

func TestOwnedOf(t *testing.T) {
	file := func(id int) []File { return []File{{ID: id}} }
	owned := []Episode{{Number: 12, Files: file(1)}, {Number: 13, Files: file(2)}, {Number: 25, Files: file(3)}}
	got := OwnedOf(Match{Status: Matched, Offset: 12}, Entry{Episodes: 12}, owned)
	if len(got) != 1 || got[1].ID != 2 {
		t.Fatalf("offset 12, 12 episodes: %+v, want only episode 1 (Shoko's 13)", got)
	}
	if got := OwnedOf(Match{Status: Partial, Offset: 12}, Entry{Episodes: 12}, owned); got != nil {
		t.Fatalf("a partial match owns %+v, want nothing until confirmed", got)
	}
	if got := OwnedOf(Match{Status: Matched}, Entry{}, owned); len(got) != 3 {
		t.Fatalf("unknown episode count: %+v, want every owned episode", got)
	}
}

// The first sync reads every series and the owned episodes of the tracked ones; after that only
// files imported since the newest one seen: a day of new files is one request when nothing is new
// for the tracked series (the Shoko spec §6).
func TestStoreSyncFullThenOnlyNewer(t *testing.T) {
	f := newFakeShoko(t)
	f.addSeries(Series{ID: 1, MAL: []int{52991}, Name: "Frieren", Episodes: 28}, nil)
	f.addSeries(Series{ID: 2, MAL: []int{777}, Name: "Untracked", Episodes: 12}, nil)
	f.addFile(10, 1, 1, t0)
	f.addFile(20, 2, 1, t0)
	c, _ := NewClient(f.server.URL, "key-1")
	dir := t.TempDir()
	store := NewStore(dir)
	tracked := map[int]bool{52991: true}
	ctx := context.Background()

	if !store.Due(f.server.URL, 24*time.Hour, t0) {
		t.Fatal("a store never synced is not due")
	}
	if err := store.Sync(ctx, c, f.server.URL, tracked, nil, false, t0); err != nil {
		t.Fatal(err)
	}
	if eps := store.OwnedEpisodes(1); len(eps) != 1 || eps[0].Number != 1 {
		t.Fatalf("owned of the tracked series = %+v", eps)
	}
	if eps := store.OwnedEpisodes(2); eps != nil {
		t.Fatalf("an untracked series' episodes were read: %+v", eps)
	}
	if store.Due(f.server.URL, 24*time.Hour, t0.Add(time.Hour)) {
		t.Fatal("due again an hour after a sync")
	}

	// Nothing new: one request.
	n := len(f.requestLog())
	if err := store.Sync(ctx, c, f.server.URL, tracked, nil, false, t0.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := f.requestLog()[n:]; len(got) != 1 || !strings.HasPrefix(got[0], "GET /api/v3/File?") {
		t.Fatalf("a sync with nothing new made %v, want one file read", got)
	}

	// A new episode of the tracked series, and a file of a series Shoko just created.
	f.addFile(11, 1, 2, t0.Add(25*time.Hour))
	f.addSeries(Series{ID: 3, MAL: []int{888}, Name: "New", Episodes: 1}, nil)
	f.addFile(30, 3, 1, t0.Add(26*time.Hour))
	tracked[888] = true
	if err := store.Sync(ctx, c, f.server.URL, tracked, nil, false, t0.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if eps := store.OwnedEpisodes(1); len(eps) != 2 {
		t.Fatalf("the new episode was not picked up: %+v", eps)
	}
	if eps := store.OwnedEpisodes(3); len(eps) != 1 {
		t.Fatalf("the new series' episode was not picked up: %+v", eps)
	}

	// Saved, and read back by a new store.
	if again := NewStore(dir).OwnedEpisodes(1); len(again) != 2 {
		t.Fatalf("the state did not survive a restart: %+v", again)
	}

	// Shoko unreachable: the state stays, the error is recorded.
	f.server.Close()
	if err := store.Sync(ctx, c, f.server.URL, tracked, nil, false, t0.Add(72*time.Hour)); err == nil {
		t.Fatal("a sync against a closed server succeeded")
	}
	snap := store.Snapshot()
	if snap.LastError == "" || len(snap.Owned[1]) != 2 {
		t.Fatalf("after a failed sync: error %q, owned %+v", snap.LastError, snap.Owned[1])
	}
}

// A newly tracked anime whose series is already known is read on the next sync, and another
// server's state is not reused.
func TestStoreSyncReadsNewlyTrackedAndStartsOverForAnotherServer(t *testing.T) {
	f := newFakeShoko(t)
	f.addSeries(Series{ID: 1, MAL: []int{52991}, Name: "Frieren", Episodes: 28}, nil)
	f.addFile(10, 1, 1, t0)
	c, _ := NewClient(f.server.URL, "key-1")
	store := NewStore(t.TempDir())
	ctx := context.Background()
	if err := store.Sync(ctx, c, f.server.URL, map[int]bool{}, nil, false, t0); err != nil {
		t.Fatal(err)
	}
	if store.OwnedEpisodes(1) != nil {
		t.Fatal("an untracked series was read")
	}
	if err := store.Sync(ctx, c, f.server.URL, map[int]bool{52991: true}, nil, false, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(store.OwnedEpisodes(1)) != 1 {
		t.Fatal("a series tracked since the last sync was not read")
	}
	if !store.Due(f.server.URL+"/other", 24*time.Hour, t0.Add(2*time.Hour)) {
		t.Fatal("another server's URL is not due")
	}
}

func TestNewStoreIgnoresAnUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, StateFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := NewStore(dir); len(s.SeriesList()) != 0 || !s.Due("http://x", time.Hour, t0) {
		t.Fatal("an unreadable state file was not treated as empty")
	}
}

// Files imported in the very instant of the newest one seen: the seen one is skipped, a new one in
// the same instant is not lost.
func TestStoreSyncSameInstantImports(t *testing.T) {
	f := newFakeShoko(t)
	f.addSeries(Series{ID: 1, MAL: []int{52991}, Name: "Frieren", Episodes: 28}, nil)
	f.addFile(10, 1, 1, t0)
	c, _ := NewClient(f.server.URL, "key-1")
	store := NewStore(t.TempDir())
	ctx := context.Background()
	tracked := map[int]bool{52991: true}
	if err := store.Sync(ctx, c, f.server.URL, tracked, nil, false, t0); err != nil {
		t.Fatal(err)
	}
	f.addFile(11, 1, 2, t0)
	if err := store.Sync(ctx, c, f.server.URL, tracked, nil, false, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if eps := store.OwnedEpisodes(1); len(eps) != 2 {
		t.Fatalf("a file imported in the same instant as the newest seen was lost: %+v", eps)
	}
	if ids := store.Snapshot().NewestImportIDs; len(ids) != 2 {
		t.Fatalf("NewestImportIDs = %v, want both files of that instant", ids)
	}
}

// A match set by hand may point at a series that shares no MAL id with any followed anime: its
// episodes are read all the same.
func TestStoreSyncReadsASeriesMatchedByHand(t *testing.T) {
	f := newFakeShoko(t)
	f.addSeries(Series{ID: 5, MAL: []int{4242}, Name: "Elsewhere", Episodes: 12}, nil)
	f.addFile(50, 5, 1, t0)
	c, _ := NewClient(f.server.URL, "key-1")
	store := NewStore(t.TempDir())
	if err := store.Sync(context.Background(), c, f.server.URL, map[int]bool{}, map[int]bool{5: true}, false, t0); err != nil {
		t.Fatal(err)
	}
	if len(store.OwnedEpisodes(5)) != 1 {
		t.Fatal("the episodes of a series matched by hand were not read")
	}
}
