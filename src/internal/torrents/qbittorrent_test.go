package torrents

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	qbHashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	qbHashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	qbHashC = "cccccccccccccccccccccccccccccccccccccccc"
)

func qbMagnet(hash string) string { return "magnet:?xt=urn:btih:" + hash }

type qbTest struct {
	fake     *fakeQBit
	backend  *QBitBackend
	root     string // the library
	savePath string // <library>/.torrents, what the daemon passes to Ensure
	settings QBitSettings
}

// newQBitTest connects a backend to a fake qBittorrent. The poll loop's ticker is set far in the
// future: the tests call poll() themselves.
func newQBitTest(t *testing.T) *qbTest {
	t.Helper()
	fake := newFakeQBit(t)
	root := t.TempDir()
	q := &qbTest{
		fake:     fake,
		root:     root,
		savePath: filepath.Join(root, ".torrents"),
		settings: QBitSettings{URL: fake.server.URL, Username: "admin", Password: "secret", Category: "aad"},
	}
	q.backend = NewQBitBackend(t.TempDir(), func() (QBitSettings, error) { return q.settings, nil })
	q.backend.pollInterval = time.Hour
	t.Cleanup(func() { _ = q.backend.Close() })
	if _, err := q.backend.Ensure(q.savePath); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	return q
}

func TestQBit_EnsureConnectsOnceThenOnlyPings(t *testing.T) {
	q := newQBitTest(t)
	if got := q.fake.callLog(); !reflect.DeepEqual(got, []string{"createCategory aad"}) {
		t.Fatalf("first connect calls = %v, want the category created", got)
	}
	created, err := q.backend.Ensure(q.savePath)
	if err != nil || created {
		t.Fatalf("second Ensure = (%v, %v), want (false, nil): same settings only ping", created, err)
	}
	q.settings.Category = "other"
	if created, err := q.backend.Ensure(q.savePath); err != nil || !created {
		t.Fatalf("Ensure after a settings change = (%v, %v), want a new connection", created, err)
	}
}

func TestQBit_EnsureRefusesOldWebAPI(t *testing.T) {
	fake := newFakeQBit(t)
	fake.webAPI = "2.8.3"
	b := NewQBitBackend(t.TempDir(), func() (QBitSettings, error) {
		return QBitSettings{URL: fake.server.URL, Username: "admin", Password: "secret"}, nil
	})
	defer b.Close()
	_, err := b.Ensure(filepath.Join(t.TempDir(), ".torrents"))
	if err == nil || !strings.Contains(err.Error(), "5.0 or later") {
		t.Fatalf("Ensure against Web API 2.8.3 = %v, want a 5.0+ error", err)
	}
}

func TestQBit_EnsureFailsWithWrongPassword(t *testing.T) {
	fake := newFakeQBit(t)
	b := NewQBitBackend(t.TempDir(), func() (QBitSettings, error) {
		return QBitSettings{URL: fake.server.URL, Username: "admin", Password: "wrong"}, nil
	})
	defer b.Close()
	if _, err := b.Ensure(filepath.Join(t.TempDir(), ".torrents")); !errors.Is(err, errQBitUnauthorized) {
		t.Fatalf("Ensure with a wrong password = %v, want errQBitUnauthorized", err)
	}
}

func TestQBit_APIKeyNeedsNoLogin(t *testing.T) {
	fake := newFakeQBit(t)
	fake.apiKey = "k3y"
	b := NewQBitBackend(t.TempDir(), func() (QBitSettings, error) {
		return QBitSettings{URL: fake.server.URL, APIKey: "k3y", Category: "aad"}, nil
	})
	defer b.Close()
	if _, err := b.Ensure(filepath.Join(t.TempDir(), ".torrents")); err != nil {
		t.Fatalf("Ensure with an API key: %v", err)
	}
	if len(fake.sessions) != 0 {
		t.Fatalf("an API key opened %d cookie sessions, want none", len(fake.sessions))
	}
}

func TestQBit_ExpiredSessionLogsInAgain(t *testing.T) {
	q := newQBitTest(t)
	q.fake.expireSessions()
	if _, err := q.backend.Ensure(q.savePath); err != nil {
		t.Fatalf("Ensure after qBittorrent forgot the session: %v", err)
	}
}

// The core of the placement: added at the library root, stopped at its metadata, renamed to its
// planned path (.part while downloading), and only then started (decisions/142).
func TestQBit_PlacedTorrentIsRenamedBeforeItStarts(t *testing.T) {
	q := newQBitTest(t)
	b := q.backend
	if err := b.SetPlacement(PlacementRequest{Hash: qbHashA, SeriesDir: "Frieren", Kind: PlacementEpisode, Season: 1,
		RenameVideo: "Frieren - S01E05", PartFiles: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Add(qbMagnet(qbHashA)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, ok := b.Get(qbHashA); !ok {
		t.Fatal("Get right after Add found nothing; Add must wait until qBittorrent lists the torrent")
	}
	q.fake.deliverMetadata(qbHashA, qbitFile{Index: 0, Name: "[Group] Frieren - 05 (1080p).mkv", Size: 10})
	b.poll()

	want := []string{
		"createCategory aad",
		"add " + qbHashA + " savepath=" + q.root + " stopAtMeta=true useDownloadPath=false",
		"rename " + qbHashA + " [Group] Frieren - 05 (1080p).mkv -> Frieren/Season 01/Frieren - S01E05.mkv.part",
		"start " + qbHashA,
	}
	if got := q.fake.callLog(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls =\n%v\nwant\n%v", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	p, _ := b.Placement(qbHashA)
	if len(p.Files) != 1 || p.Files[0].Path != filepath.Join(q.root, "Frieren", "Season 01", "Frieren - S01E05.mkv") {
		t.Fatalf("placement files = %+v", p.Files)
	}
	files, err := b.Files(qbHashA)
	if err != nil || len(files) != 1 || files[0].Path != "[Group] Frieren - 05 (1080p).mkv" {
		t.Fatalf("Files = %+v, %v; want the raw name from the metadata", files, err)
	}

	// A second poll finds the names right and calls nothing.
	before := len(q.fake.callLog())
	b.poll()
	if got := q.fake.callLog(); len(got) != before {
		t.Fatalf("a poll with the names already applied called %v", got[before:])
	}
}

func TestQBit_CompletionFiresOnceThenFinalizeRenamesPartFiles(t *testing.T) {
	q := newQBitTest(t)
	b := q.backend
	var completed []string
	b.SetCallbacks(func(hash string) { completed = append(completed, hash) }, nil)
	_ = b.SetPlacement(PlacementRequest{Hash: qbHashA, SeriesDir: "Frieren", Kind: PlacementEpisode, Season: 1, PartFiles: true})
	if _, err := b.Add(qbMagnet(qbHashA)); err != nil {
		t.Fatal(err)
	}
	q.fake.deliverMetadata(qbHashA, qbitFile{Index: 0, Name: "ep05.mkv", Size: 10})
	b.poll()
	if err := b.FinalizePlacement(qbHashA); err == nil {
		t.Fatal("FinalizePlacement of an unfinished torrent succeeded")
	}

	q.fake.finish(qbHashA)
	b.poll()
	b.poll()
	if !reflect.DeepEqual(completed, []string{qbHashA}) {
		t.Fatalf("onComplete calls = %v, want exactly one", completed)
	}

	if err := b.FinalizePlacement(qbHashA); err != nil {
		t.Fatalf("FinalizePlacement: %v", err)
	}
	final := filepath.Join(q.root, "Frieren", "Season 01", "ep05.mkv")
	if !hasSize(final, 10) || pathExists(final+partSuffix) {
		t.Fatalf("after finalize: final exists=%v, .part exists=%v", hasSize(final, 10), pathExists(final+partSuffix))
	}
	if p, _ := b.Placement(qbHashA); !p.Completed {
		t.Fatal("placement not marked completed")
	}
	// Idempotent.
	if err := b.FinalizePlacement(qbHashA); err != nil {
		t.Fatalf("second FinalizePlacement: %v", err)
	}
}

func TestQBit_TorrentFirstSeenCompleteFiresNothing(t *testing.T) {
	fake := newFakeQBit(t)
	fake.put(qbitTorrent{Hash: qbHashA, Category: "aad", State: "stalledUP", Progress: 1, TotalSize: 5, Size: 5})
	b := NewQBitBackend(t.TempDir(), func() (QBitSettings, error) {
		return QBitSettings{URL: fake.server.URL, Username: "admin", Password: "secret", Category: "aad"}, nil
	})
	b.pollInterval = time.Hour
	defer b.Close()
	fired := 0
	b.SetCallbacks(func(string) { fired++ }, func(string, error) { fired++ })
	if _, err := b.Ensure(filepath.Join(t.TempDir(), ".torrents")); err != nil {
		t.Fatal(err)
	}
	b.poll()
	if fired != 0 {
		t.Fatalf("a torrent already complete at connect fired %d callbacks; the reconcile pass handles it", fired)
	}
	// One that shows up complete later (put in the category from outside the app) doesn't either.
	fake.put(qbitTorrent{Hash: qbHashB, Category: "aad", State: "stalledUP", Progress: 1, TotalSize: 5, Size: 5})
	b.poll()
	if fired != 0 {
		t.Fatalf("a torrent first seen complete after connect fired %d callbacks", fired)
	}
	if info, ok := b.Get(qbHashA); !ok || !info.Completed || info.Status != "seeding" {
		t.Fatalf("Get = %+v, %v", info, ok)
	}
}

// Files deleted outside the app: qBittorrent says missingFiles, the daemon must hear the same
// ErrPlacedFileMissing rain reports (decisions/139).
func TestQBit_MissingFilesOfAFinishedPlacement(t *testing.T) {
	q := newQBitTest(t)
	b := q.backend
	var failures []error
	b.SetCallbacks(nil, func(_ string, err error) { failures = append(failures, err) })
	_ = b.SetPlacement(PlacementRequest{Hash: qbHashA, SeriesDir: "Frieren", Kind: PlacementEpisode, Season: 1})
	_, _ = b.Add(qbMagnet(qbHashA))
	q.fake.deliverMetadata(qbHashA, qbitFile{Index: 0, Name: "ep.mkv", Size: 4})
	b.poll()
	q.fake.finish(qbHashA)
	b.poll()
	if err := b.FinalizePlacement(qbHashA); err != nil {
		t.Fatal(err)
	}
	q.fake.setState(qbHashA, "missingFiles")
	b.poll()
	b.poll()
	if len(failures) != 1 || !errors.Is(failures[0], ErrPlacedFileMissing) {
		t.Fatalf("failures = %v, want one ErrPlacedFileMissing", failures)
	}
	if info, _ := b.Get(qbHashA); !errors.Is(info.StopError, ErrPlacedFileMissing) || info.Status != StatusStopped {
		t.Fatalf("Get = status %q, StopError %v", info.Status, info.StopError)
	}
}

func TestQBit_RemoveDeletesPlacedFilesItself(t *testing.T) {
	q := newQBitTest(t)
	b := q.backend
	_ = b.SetPlacement(PlacementRequest{Hash: qbHashA, SeriesDir: "Frieren", Kind: PlacementEpisode, Season: 1})
	_, _ = b.Add(qbMagnet(qbHashA))
	q.fake.deliverMetadata(qbHashA, qbitFile{Index: 0, Name: "ep.mkv", Size: 4})
	b.poll()
	q.fake.finish(qbHashA)
	final := filepath.Join(q.root, "Frieren", "Season 01", "ep.mkv")
	if !hasSize(final, 4) {
		t.Fatal("setup: the file is not at its planned path")
	}

	if err := b.Remove(qbHashA, false); err != nil {
		t.Fatal(err)
	}
	log := q.fake.callLog()
	if last := log[len(log)-1]; last != "delete "+qbHashA+" deleteFiles=false" {
		t.Fatalf("last call = %q, want qBittorrent to drop the torrent only", last)
	}
	if pathExists(final) || pathExists(filepath.Join(q.root, "Frieren")) {
		t.Fatal("the placed file or its emptied folders are still there")
	}
	if _, ok := b.Placement(qbHashA); ok {
		t.Fatal("placement not forgotten")
	}
	if _, ok := b.Get(qbHashA); ok {
		t.Fatal("removed torrent still listed")
	}
}

func TestQBit_RemoveKeepDataKeepsFilesAndPlacement(t *testing.T) {
	q := newQBitTest(t)
	b := q.backend
	_ = b.SetPlacement(PlacementRequest{Hash: qbHashA, SeriesDir: "Frieren", Kind: PlacementEpisode, Season: 1})
	_, _ = b.Add(qbMagnet(qbHashA))
	q.fake.deliverMetadata(qbHashA, qbitFile{Index: 0, Name: "ep.mkv", Size: 4})
	b.poll()
	q.fake.finish(qbHashA)
	if err := b.Remove(qbHashA, true); err != nil {
		t.Fatal(err)
	}
	if !hasSize(filepath.Join(q.root, "Frieren", "Season 01", "ep.mkv"), 4) {
		t.Fatal("keepData removed the file")
	}
	if _, ok := b.Placement(qbHashA); !ok {
		t.Fatal("keepData forgot the placement; a re-add must resume into the same files")
	}
}

func TestQBit_OnlyTheAppsCategoryIsListed(t *testing.T) {
	fake := newFakeQBit(t)
	fake.put(qbitTorrent{Hash: qbHashA, Category: "aad", State: "downloading", TotalSize: 1})
	fake.put(qbitTorrent{Hash: qbHashB, Category: "movies", State: "downloading", TotalSize: 1})
	b := NewQBitBackend(t.TempDir(), func() (QBitSettings, error) {
		return QBitSettings{URL: fake.server.URL, Username: "admin", Password: "secret", Category: "aad"}, nil
	})
	b.pollInterval = time.Hour
	defer b.Close()
	if _, err := b.Ensure(filepath.Join(t.TempDir(), ".torrents")); err != nil {
		t.Fatal(err)
	}
	list := b.List()
	if len(list) != 1 || list[0].Hash != qbHashA {
		t.Fatalf("List = %+v, want only the app's category", list)
	}
	if _, ok := b.Get(qbHashB); ok {
		t.Fatal("Get found a torrent of another category")
	}
}

func TestQBit_PathMapAppliesBothWays(t *testing.T) {
	q := newQBitTest(t)
	q.settings.PathMap = []QBitPathMapping{{App: q.root, QBittorrent: "/downloads"}}
	if _, err := q.backend.Ensure(q.savePath); err != nil {
		t.Fatal(err)
	}
	_ = q.backend.SetPlacement(PlacementRequest{Hash: qbHashA, SeriesDir: "Frieren", Kind: PlacementPack})
	if _, err := q.backend.Add(qbMagnet(qbHashA)); err != nil {
		t.Fatal(err)
	}
	if got := q.fake.torrent(qbHashA).SavePath; got != "/downloads" {
		t.Fatalf("qBittorrent got savepath %q, want the mapped /downloads", got)
	}
	if info, _ := q.backend.Get(qbHashA); info.DataDir != q.root {
		t.Fatalf("DataDir = %q, want it mapped back to %q", info.DataDir, q.root)
	}
}

func TestQBitMapPath(t *testing.T) {
	maps := []QBitPathMapping{{App: "/mnt/anime", QBittorrent: "/data"}, {App: "/mnt/anime/movies", QBittorrent: "/movies/"}}
	cases := []struct {
		path   string
		toQBit bool
		want   string
	}{
		{"/mnt/anime/Frieren", true, "/data/Frieren"},
		{"/mnt/anime/movies/X", true, "/movies/X"}, // the longest prefix wins
		{"/mnt/anime", true, "/data"},
		{"/mnt/animeX/y", true, "/mnt/animeX/y"}, // a prefix of the name, not of the folder
		{"/data/Frieren", false, "/mnt/anime/Frieren"},
		{"/elsewhere", false, "/elsewhere"},
	}
	for _, c := range cases {
		if got := qbitMapPath(c.path, maps, c.toQBit); got != c.want {
			t.Errorf("qbitMapPath(%q, toQBit=%v) = %q, want %q", c.path, c.toQBit, got, c.want)
		}
	}
	if got := qbitMapPath("/x", nil, true); got != "/x" {
		t.Errorf("no map changed the path: %q", got)
	}
}

func TestQBitStatusSlug(t *testing.T) {
	cases := map[string]string{
		"metaDL": StatusDownloadingMetadata, "forcedMetaDL": StatusDownloadingMetadata,
		"downloading": StatusDownloading, "stalledDL": StatusDownloading, "forcedDL": StatusDownloading,
		"queuedDL": StatusQueued, "uploading": "seeding", "stalledUP": "seeding", "queuedUP": "seeding",
		"forcedUP": "seeding", "checkingDL": "verifying", "checkingUP": "verifying",
		"checkingResumeData": "verifying", "moving": "verifying", "stoppedDL": StatusStopped,
		"stoppedUP": StatusStopped, "pausedDL": StatusStopped, "error": StatusStopped,
		"missingFiles": StatusStopped, "allocating": "allocating", "somethingNew": "unknown",
	}
	for state, want := range cases {
		if got := qbitStatusSlug(state); got != want {
			t.Errorf("qbitStatusSlug(%q) = %q, want %q", state, got, want)
		}
	}
}

func TestQBit_PrioritizeAllKeepsTheOrderReceived(t *testing.T) {
	q := newQBitTest(t)
	for _, h := range []string{qbHashA, qbHashB, qbHashC} {
		if _, err := q.backend.Add(qbMagnet(h)); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.backend.PrioritizeAll([]string{qbHashB, qbHashA, qbHashC}); err != nil {
		t.Fatal(err)
	}
	var top []string
	for _, c := range q.fake.callLog() {
		if strings.HasPrefix(c, "topPrio ") {
			top = append(top, strings.TrimPrefix(c, "topPrio "))
		}
	}
	// Each topPrio goes in front of the previous one: last first ends with B on top.
	if want := []string{qbHashC, qbHashA, qbHashB}; !reflect.DeepEqual(top, want) {
		t.Fatalf("topPrio order = %v, want %v", top, want)
	}

	q.fake.queueing = false
	if err := q.backend.PrioritizeAll([]string{qbHashA}); err != nil {
		t.Fatalf("PrioritizeAll with queueing off = %v; qBittorrent's 409 means there is no queue, not an error", err)
	}
}

func TestQBit_PauseWhileAwaitingRenamesStaysStopped(t *testing.T) {
	q := newQBitTest(t)
	b := q.backend
	_ = b.SetPlacement(PlacementRequest{Hash: qbHashA, SeriesDir: "Frieren", Kind: PlacementEpisode, Season: 1})
	_, _ = b.Add(qbMagnet(qbHashA))
	if err := b.Pause(qbHashA); err != nil {
		t.Fatal(err)
	}
	q.fake.deliverMetadata(qbHashA, qbitFile{Index: 0, Name: "ep.mkv", Size: 4})
	b.poll()
	for _, c := range q.fake.callLog() {
		if strings.HasPrefix(c, "start ") {
			t.Fatalf("a torrent the user paused was started after its renames: %v", q.fake.callLog())
		}
	}
	// Resume before the renames happened starts it once they are done, not before.
	_ = b.SetPlacement(PlacementRequest{Hash: qbHashB, SeriesDir: "Frieren", Kind: PlacementEpisode, Season: 2})
	_, _ = b.Add(qbMagnet(qbHashB))
	_ = b.Pause(qbHashB)
	if err := b.Resume(qbHashB); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(q.fake.callLog()[len(q.fake.callLog())-1], "start ") {
		t.Fatal("Resume started a torrent whose files are not renamed yet")
	}
	q.fake.deliverMetadata(qbHashB, qbitFile{Index: 0, Name: "ep2.mkv", Size: 4})
	b.poll()
	if last := q.fake.callLog()[len(q.fake.callLog())-1]; last != "start "+qbHashB {
		t.Fatalf("last call = %q, want the resumed torrent started after its renames", last)
	}
}

func TestQBit_RedownloadRechecksAndStarts(t *testing.T) {
	q := newQBitTest(t)
	b := q.backend
	_ = b.SetPlacement(PlacementRequest{Hash: qbHashA, SeriesDir: "Show", Kind: PlacementPack, PartFiles: true})
	_, _ = b.Add(qbMagnet(qbHashA))
	q.fake.deliverMetadata(qbHashA,
		qbitFile{Index: 0, Name: "Pack/01.mkv", Size: 3},
		qbitFile{Index: 1, Name: "Pack/02.mkv", Size: 3})
	b.poll()
	q.fake.finish(qbHashA)
	b.poll()
	if err := b.FinalizePlacement(qbHashA); err != nil {
		t.Fatal(err)
	}
	// 02 is deleted outside the app; the user asks to redownload the missing files.
	if err := os.Remove(filepath.Join(q.root, "Show", "Pack", "02.mkv")); err != nil {
		t.Fatal(err)
	}
	var completed []string
	b.SetCallbacks(func(hash string) { completed = append(completed, hash) }, nil)
	if err := b.RedownloadPlacement(qbHashA, ""); err != nil {
		t.Fatal(err)
	}
	before := len(q.fake.callLog())
	b.poll()
	got := q.fake.callLog()[before:]
	want := []string{
		"rename " + qbHashA + " Show/Pack/02.mkv -> Show/Pack/02.mkv.part",
		"recheck " + qbHashA,
		"start " + qbHashA,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("redownload calls = %v, want %v (the whole file keeps its name)", got, want)
	}

	// The poll still sees qBittorrent's state from before the recheck (complete): that must neither
	// fire onComplete nor let FinalizePlacement rename the new .part back (decisions/140).
	if len(completed) != 0 {
		t.Fatalf("onComplete fired from the stale complete state: %v", completed)
	}
	if err := b.FinalizePlacement(qbHashA); err == nil {
		t.Fatal("FinalizePlacement accepted a torrent whose recheck hasn't started")
	}
	part := filepath.Join(q.root, "Show", "Pack", "02.mkv.part")
	q.fake.mu.Lock()
	q.fake.torrents[qbHashA].Progress = 0.5
	q.fake.torrents[qbHashA].State = "downloading"
	q.fake.mu.Unlock()
	b.poll()
	q.fake.finish(qbHashA)
	b.poll()
	if !reflect.DeepEqual(completed, []string{qbHashA}) {
		t.Fatalf("onComplete calls after the real redownload = %v, want one", completed)
	}
	if !hasSize(part, 3) {
		t.Fatal("the missing file did not download under its .part name")
	}
	if err := b.FinalizePlacement(qbHashA); err != nil {
		t.Fatalf("FinalizePlacement after the redownload: %v", err)
	}
	if !hasSize(filepath.Join(q.root, "Show", "Pack", "02.mkv"), 3) {
		t.Fatal("the redownloaded file did not get its final name")
	}
	if err := b.RedownloadPlacement(qbHashA, "/fast/incomplete"); !errors.Is(err, errQBitStaging) {
		t.Fatalf("redownload into an incomplete folder = %v, want errQBitStaging", err)
	}
}

func TestQBit_MergeAppliesPartialUpdatesAndRemovals(t *testing.T) {
	b := NewQBitBackend(t.TempDir(), nil)
	b.current.Category = "aad"
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	b.mergeLocked(qbitMainData{RID: 1, FullUpdate: true, Torrents: map[string]map[string]json.RawMessage{
		qbHashA: {"name": raw(`"A"`), "category": raw(`"aad"`), "state": raw(`"downloading"`), "progress": raw(`0.5`)},
		qbHashB: {"name": raw(`"B"`), "category": raw(`"aad"`), "state": raw(`"downloading"`)},
	}})
	b.mergeLocked(qbitMainData{RID: 2, Torrents: map[string]map[string]json.RawMessage{
		qbHashA: {"state": raw(`"stalledUP"`), "progress": raw(`1`)},
	}, TorrentsRemoved: []string{qbHashB}})
	own := b.ownTorrentsLocked()
	if len(own) != 1 || own[qbHashA].Name != "A" || own[qbHashA].State != "stalledUP" || own[qbHashA].Progress != 1 {
		t.Fatalf("after a partial update and a removal: %+v", own)
	}
	if b.rid != 2 {
		t.Fatalf("rid = %d, want 2", b.rid)
	}
}

func TestWebAPIAtLeast(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{{"2.11", true}, {"2.11.3", true}, {"2.12.0", true}, {"3.0", true}, {"2.10.4", false}, {"2.8.3", false}, {"v2.11.0", true}}
	for _, c := range cases {
		if got := webAPIAtLeast(c.version, "2.11"); got != c.want {
			t.Errorf("webAPIAtLeast(%q) = %v, want %v", c.version, got, c.want)
		}
	}
}

func TestTestQBittorrent_ProbesTheLibrary(t *testing.T) {
	fake := newFakeQBit(t)
	library := t.TempDir()
	settings := QBitSettings{URL: fake.server.URL, Username: "admin", Password: "secret"}

	res, err := TestQBittorrent(settings, library)
	if err != nil {
		t.Fatal(err)
	}
	if res.WebAPIVersion != "2.11.3" || res.AppVersion != "v5.1.0" || !res.LibraryChecked || !res.LibraryVisible || res.Problem != "" {
		t.Fatalf("same folder on both sides: %+v", res)
	}
	if entries, _ := os.ReadDir(library); len(entries) != 0 {
		t.Fatalf("the probe file was left in the library: %v", entries)
	}

	// qBittorrent pointed at another folder through the path map.
	settings.PathMap = []QBitPathMapping{{App: library, QBittorrent: t.TempDir()}}
	res, err = TestQBittorrent(settings, library)
	if err != nil {
		t.Fatal(err)
	}
	if res.LibraryVisible || res.Problem == "" {
		t.Fatalf("different folders: %+v, want a problem reported", res)
	}

	settings.Password = "wrong"
	if _, err := TestQBittorrent(settings, library); err == nil {
		t.Fatal("a wrong password passed the test")
	}
}

// The daemon reads PiecesHave > 0 as "data worth keeping" when it drops a failed torrent
// (decisions/024). qBittorrent has no piece counts: progress stands in, and any progress counts.
func TestQBit_ProgressStandsInForPieces(t *testing.T) {
	q := newQBitTest(t)
	q.fake.put(qbitTorrent{Hash: qbHashA, Category: "aad", State: "error", Progress: 0.00001, TotalSize: 9, Size: 9})
	q.fake.put(qbitTorrent{Hash: qbHashB, Category: "aad", State: "stalledUP", Progress: 1, TotalSize: 9, Size: 9})
	q.backend.poll()
	a, _ := q.backend.Get(qbHashA)
	if a.PiecesHave == 0 || a.PiecesTotal != qbitProgressScale {
		t.Fatalf("a failed torrent with some progress: PiecesHave %d / %d, want > 0", a.PiecesHave, a.PiecesTotal)
	}
	if b, _ := q.backend.Get(qbHashB); b.PiecesHave != qbitProgressScale {
		t.Fatalf("a complete torrent: PiecesHave %d, want %d", b.PiecesHave, qbitProgressScale)
	}

	// No metadata, no data.
	_, _ = q.backend.Add(qbMagnet(qbHashC))
	if c, _ := q.backend.Get(qbHashC); c.PiecesHave != 0 || c.PiecesTotal != 0 {
		t.Fatalf("a torrent without metadata: %d / %d, want 0 / 0", c.PiecesHave, c.PiecesTotal)
	}
}

// A Kept placement re-added and dropped before qBittorrent got its metadata keeps the user's files,
// even though an earlier run froze its file map (decisions/137).
func TestQBit_RemoveKeptBeforeMetadataKeepsFiles(t *testing.T) {
	q := newQBitTest(t)
	b := q.backend
	_ = b.SetPlacement(PlacementRequest{Hash: qbHashA, SeriesDir: "Frieren", Kind: PlacementEpisode, Season: 1})
	_, _ = b.Add(qbMagnet(qbHashA))
	q.fake.deliverMetadata(qbHashA, qbitFile{Index: 0, Name: "ep.mkv", Size: 4})
	b.poll()
	q.fake.finish(qbHashA)
	b.poll()
	if err := b.FinalizePlacement(qbHashA); err != nil {
		t.Fatal(err)
	}
	if err := b.SetPlacementKept(qbHashA, true); err != nil {
		t.Fatal(err)
	}
	if err := b.Remove(qbHashA, true); err != nil {
		t.Fatal(err)
	}

	// Re-added, stuck before its metadata, then dropped without keepData (a stall, a failure).
	if _, err := b.Add(qbMagnet(qbHashA)); err != nil {
		t.Fatal(err)
	}
	if err := b.Remove(qbHashA, false); err != nil {
		t.Fatal(err)
	}
	if !hasSize(filepath.Join(q.root, "Frieren", "Season 01", "ep.mkv"), 4) {
		t.Fatal("the Kept placement's file was deleted")
	}
	if _, ok := b.Placement(qbHashA); !ok {
		t.Fatal("the Kept placement was forgotten")
	}
}

// A library replaced while the app runs is reported on the next pass, once (decisions/034).
func TestQBit_LibrarySwapIsReportedOnTheNextPass(t *testing.T) {
	q := newQBitTest(t)
	if q.backend.ConsumeRootSwap() {
		t.Fatal("swap reported on the first connect")
	}
	if err := os.Remove(filepath.Join(q.savePath, RootMarkerName)); err != nil {
		t.Fatal(err)
	}
	if _, err := q.backend.Ensure(q.savePath); err != nil {
		t.Fatal(err)
	}
	if !q.backend.ConsumeRootSwap() {
		t.Fatal("the replaced library was not reported while connected")
	}
	if _, err := q.backend.Ensure(q.savePath); err != nil {
		t.Fatal(err)
	}
	if q.backend.ConsumeRootSwap() {
		t.Fatal("the same swap was reported twice")
	}
}

// qBittorrent 5.1+ (Web API 2.14+) answers differently: 204 for a successful login and for actions,
// 401 for a refused login, JSON with counts (202 while pending) for torrents/add. Measured against
// qBittorrent 5.3.0, whose login answered 204 with no body.
func TestQBit_ModernResponseFormats(t *testing.T) {
	fake := newFakeQBit(t)
	fake.modern = true
	root := t.TempDir()
	settings := QBitSettings{URL: fake.server.URL, Username: "admin", Password: "secret", Category: "aad"}
	b := NewQBitBackend(t.TempDir(), func() (QBitSettings, error) { return settings, nil })
	b.pollInterval = time.Hour
	defer b.Close()
	if _, err := b.Ensure(filepath.Join(root, ".torrents")); err != nil {
		t.Fatalf("Ensure against a 204 login: %v", err)
	}
	_ = b.SetPlacement(PlacementRequest{Hash: qbHashA, SeriesDir: "Frieren", Kind: PlacementEpisode, Season: 1, PartFiles: true})
	if _, err := b.Add(qbMagnet(qbHashA)); err != nil {
		t.Fatalf("Add against a 202 JSON answer: %v", err)
	}
	fake.deliverMetadata(qbHashA, qbitFile{Index: 0, Name: "ep.mkv", Size: 4})
	b.poll()
	if last := fake.callLog()[len(fake.callLog())-1]; last != "start "+qbHashA {
		t.Fatalf("204 answers broke the rename-then-start flow; last call %q", last)
	}
	if _, err := b.Add("magnet:?xt=urn:btih:" + strings.Repeat("0", 39) + "z"); err == nil {
		t.Fatal("an invalid magnet was accepted")
	}

	settings.Password = "wrong"
	b2 := NewQBitBackend(t.TempDir(), func() (QBitSettings, error) { return settings, nil })
	defer b2.Close()
	_, err := b2.Ensure(filepath.Join(root, ".torrents"))
	if !errors.Is(err, errQBitUnauthorized) || !strings.Contains(err.Error(), "same port") {
		t.Fatalf("a 401 login = %v, want errQBitUnauthorized naming the Docker port cause", err)
	}
}

func TestQBitAddResult(t *testing.T) {
	cases := map[string]bool{
		"Ok.":    true,
		"Fails.": false,
		`{"success_count":1,"pending_count":0,"failure_count":0,"added_torrent_ids":["x"]}`: true,
		`{"success_count":0,"pending_count":1,"failure_count":0,"added_torrent_ids":[]}`:    true,
		`{"success_count":0,"pending_count":0,"failure_count":1,"added_torrent_ids":[]}`:    false,
	}
	for body, ok := range cases {
		if err := qbitAddResult([]byte(body)); (err == nil) != ok {
			t.Errorf("qbitAddResult(%s) = %v, want ok=%v", body, err, ok)
		}
	}
}
