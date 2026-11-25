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
		"add " + qbHashA + " savepath=" + q.root + " stopAtMeta=true useDownloadPath=false limits=-1/-1/-1 upLimit= downloadPath=",
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
		"checkingResumeData": "verifying", "moving": StatusMoving, "stoppedDL": StatusStopped,
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
	// With incomplete_path set it still redownloads in place, and qBittorrent's download path is
	// cleared first: set while the recheck makes the torrent incomplete, it would move the whole
	// pack out of the library (decisions/145).
	if err := os.Remove(filepath.Join(q.root, "Show", "Pack", "01.mkv")); err != nil {
		t.Fatal(err)
	}
	before = len(q.fake.callLog())
	if err := b.RedownloadPlacement(qbHashA, "/fast/incomplete"); err != nil {
		t.Fatalf("redownload with an incomplete folder set: %v", err)
	}
	if got := q.fake.callLog()[before:]; !reflect.DeepEqual(got, []string{"setDownloadPath " + qbHashA + " "}) {
		t.Fatalf("redownload calls = %v, want the download path cleared", got)
	}
	if p, _ := b.Placement(qbHashA); p.IncompleteRoot != "" || p.Completed {
		t.Fatalf("placement after the redownload = %+v, want reopened in place", p)
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

	res, err := TestQBittorrent(settings, library, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.WebAPIVersion != "2.11.3" || res.AppVersion != "v5.1.0" || !res.LibraryChecked || !res.LibraryVisible || res.Problem != "" ||
		res.SignIn != "password" || res.DiskIOType == nil || *res.DiskIOType != qbitDiskIODefault {
		t.Fatalf("same folder on both sides: %+v", res)
	}
	if entries, _ := os.ReadDir(library); len(entries) != 0 {
		t.Fatalf("the probe file was left in the library: %v", entries)
	}
	// qBittorrent's own queue, what applies when the app doesn't manage it (decisions/144).
	if res.QueueingEnabled == nil || !*res.QueueingEnabled || res.MaxActiveDownloads == nil || *res.MaxActiveDownloads != 3 {
		t.Fatalf("queue = %v / %v, want qBittorrent's 3 at a time", res.QueueingEnabled, res.MaxActiveDownloads)
	}

	// The incomplete folder is probed too, once the library is seen.
	incomplete := t.TempDir()
	res, err = TestQBittorrent(settings, library, incomplete)
	if err != nil || !res.IncompleteChecked || !res.IncompleteVisible || res.IncompletePath != incomplete || res.Problem != "" {
		t.Fatalf("incomplete folder on both sides: %+v, %v", res, err)
	}
	settings.PathMap = []QBitPathMapping{{App: library, QBittorrent: library}, {App: incomplete, QBittorrent: t.TempDir()}}
	res, _ = TestQBittorrent(settings, library, incomplete)
	if !res.LibraryVisible || res.IncompleteVisible || !strings.Contains(res.Problem, "incomplete folder") {
		t.Fatalf("incomplete folder mapped elsewhere: %+v, want its problem reported", res)
	}

	// qBittorrent pointed at another folder through the path map.
	settings.PathMap = []QBitPathMapping{{App: library, QBittorrent: t.TempDir()}}
	res, err = TestQBittorrent(settings, library, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.LibraryVisible || res.Problem == "" {
		t.Fatalf("different folders: %+v, want a problem reported", res)
	}

	settings.Password = "wrong"
	if _, err := TestQBittorrent(settings, library, ""); err == nil {
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

// callsSince returns the fake's calls after the first n, so a test reads only what a step called.
func (q *qbTest) callsSince(n int) []string {
	return q.fake.callLog()[n:]
}

// Every torrent of the app is added with no share limit: qBittorrent's global one would stop it, or
// remove it with its files, which are the library. Measured on qBittorrent 5.3.0: under a global
// ratio limit of 0 a torrent left on "use the global limit" stopped the moment it finished
// (decisions/144).
func TestQBit_AddGivesNoShareLimitAndTheUploadFloorOnlyWithDisableSeeding(t *testing.T) {
	q := newQBitTest(t)
	if _, err := q.backend.Add(qbMagnet(qbHashA)); err != nil {
		t.Fatal(err)
	}
	q.backend.SetUploadDisabled(true)
	if _, err := q.backend.Add(qbMagnet(qbHashB)); err != nil {
		t.Fatal(err)
	}
	log := strings.Join(q.fake.callLog(), "\n")
	for _, want := range []string{
		"add " + qbHashA + " savepath=" + q.savePath + " stopAtMeta=false useDownloadPath=false limits=-1/-1/-1 upLimit= downloadPath=\n",
		"add " + qbHashB + " savepath=" + q.savePath + " stopAtMeta=false useDownloadPath=false limits=-1/-1/-1 upLimit=16384 downloadPath=",
	} {
		if !strings.Contains(log+"\n", want) {
			t.Errorf("calls =\n%s\nwant a line %q", log, want)
		}
	}
	// The poll only puts the floor on A, added before disable_seeding was on: B has it already.
	n := len(q.fake.callLog())
	q.backend.poll()
	if got := q.callsSince(n); !reflect.DeepEqual(got, []string{"setUploadLimit " + qbHashA + " 16384"}) {
		t.Fatalf("a poll right after the adds called %v, want only A's floor", got)
	}
}

// A torrent of the app that has a share limit anyway (taken over from another category, added by
// hand in the category, or changed in qBittorrent) loses it on the next poll, whatever the limit.
// Other categories are not the app's.
func TestQBit_ShareLimitsAreTakenOffTheAppsTorrents(t *testing.T) {
	for _, modern := range []bool{false, true} {
		t.Run(map[bool]string{false: "5.0", true: "5.3"}[modern], func(t *testing.T) {
			q := newQBitTest(t)
			q.fake.modern = modern
			global, custom := -2.0, 1.5
			q.fake.put(qbitTorrent{Hash: qbHashA, Category: "aad", State: "stalledUP", Progress: 1, TotalSize: 4, RatioLimit: &global})
			q.fake.put(qbitTorrent{Hash: qbHashB, Category: "aad", State: "stalledUP", Progress: 1, TotalSize: 4, RatioLimit: &custom})
			q.fake.put(qbitTorrent{Hash: qbHashC, Category: "theirs", State: "stalledUP", Progress: 1, TotalSize: 4, RatioLimit: &global})
			n := len(q.fake.callLog())
			q.backend.poll()
			want := []string{"setShareLimits " + qbHashA + "," + qbHashB + " -1/-1/-1"}
			if got := q.callsSince(n); !reflect.DeepEqual(got, want) {
				t.Fatalf("calls = %v, want %v", got, want)
			}
			if a := q.fake.torrent(qbHashA); *a.RatioLimit != -1 || *a.SeedingTimeLimit != -1 || *a.InactiveSeedingTimeLimit != -1 {
				t.Fatalf("qBittorrent did not take the call: %v %v %v", *a.RatioLimit, *a.SeedingTimeLimit, *a.InactiveSeedingTimeLimit)
			}
			n = len(q.fake.callLog())
			q.backend.poll()
			if got := q.callsSince(n); len(got) != 0 {
				t.Fatalf("a poll with the limits already off called %v", got)
			}
		})
	}
}

// A qBittorrent that answers the limit calls without applying them is not sent the same call on
// every poll: once a minute at most.
func TestQBit_ALimitQBittorrentIgnoresIsResentOnceAMinute(t *testing.T) {
	q := newQBitTest(t)
	q.fake.ignoreLimits = true
	global := -2.0
	q.fake.put(qbitTorrent{Hash: qbHashA, Category: "aad", State: "stalledUP", Progress: 1, TotalSize: 4, RatioLimit: &global})
	n := len(q.fake.callLog())
	q.backend.poll()
	q.backend.poll()
	if got := q.callsSince(n); len(got) != 1 {
		t.Fatalf("two polls called %v, want one setShareLimits", got)
	}
	q.backend.mu.Lock()
	q.backend.shareSent[qbHashA] = qbitSent{limit: -1, at: time.Now().Add(-qbitLimitResend)}
	q.backend.mu.Unlock()
	q.backend.poll()
	if got := q.callsSince(n); len(got) != 2 {
		t.Fatalf("after a minute the calls are %v, want the call sent again", got)
	}
}

// disable_seeding with qBittorrent is "almost no upload": every torrent of the app is held at the
// floor (16 KiB/s by default), whoever changed it, since 0 means unlimited there. Turning it off
// lifts the floor and leaves a limit the user chose alone (decisions/119, decisions/144).
func TestQBit_DisableSeedingHoldsEveryTorrentAtTheUploadFloor(t *testing.T) {
	q := newQBitTest(t)
	b := q.backend
	var none, userLimit int64 = 0, 50000
	q.fake.put(qbitTorrent{Hash: qbHashA, Category: "aad", State: "downloading", TotalSize: 4, UpLimit: &none})
	q.fake.put(qbitTorrent{Hash: qbHashB, Category: "aad", State: "stalledUP", Progress: 1, TotalSize: 4, UpLimit: &userLimit})
	q.fake.put(qbitTorrent{Hash: qbHashC, Category: "theirs", State: "downloading", TotalSize: 4, UpLimit: &none})
	b.poll()

	n := len(q.fake.callLog())
	b.SetUploadDisabled(true)
	b.poll()
	want := []string{"setUploadLimit " + qbHashA + "," + qbHashB + " 16384"}
	if got := q.callsSince(n); !reflect.DeepEqual(got, want) {
		t.Fatalf("turning disable_seeding on called %v, want %v", got, want)
	}
	n = len(q.fake.callLog())
	b.poll()
	if got := q.callsSince(n); len(got) != 0 {
		t.Fatalf("a poll with the floor in place called %v", got)
	}

	// The user lifts it in qBittorrent: it comes back.
	q.fake.setLimits(qbHashA, -1, 0)
	b.mu.Lock()
	b.uploadSent[qbHashA] = qbitSent{limit: 16384, at: time.Now().Add(-qbitLimitResend)}
	b.mu.Unlock()
	n = len(q.fake.callLog())
	b.poll()
	if got := q.callsSince(n); !reflect.DeepEqual(got, []string{"setUploadLimit " + qbHashA + " 16384"}) {
		t.Fatalf("a floor lifted by hand: calls %v, want it set again", got)
	}

	// Off again: the floor goes, right away, and only from the torrents that carry it.
	q.fake.setLimits(qbHashB, -1, userLimit)
	n = len(q.fake.callLog())
	b.SetUploadDisabled(false)
	b.poll()
	if got := q.callsSince(n); !reflect.DeepEqual(got, []string{"setUploadLimit " + qbHashA + " 0"}) {
		t.Fatalf("turning disable_seeding off called %v, want only A's floor lifted", got)
	}
}

// qbittorrent_disable_seeding_upload_kib changed while disable_seeding is on moves every torrent to
// the new floor; turned off in the same save as another change of the floor, the old one is still
// lifted.
func TestQBit_AChangedUploadFloorAppliesAndIsLiftedLater(t *testing.T) {
	q := newQBitTest(t)
	b := q.backend
	var none int64
	q.fake.put(qbitTorrent{Hash: qbHashA, Category: "aad", State: "downloading", TotalSize: 4, UpLimit: &none})
	b.SetUploadDisabled(true)
	b.poll()

	q.settings.UploadFloorKiB = 32
	if _, err := b.Ensure(q.savePath); err != nil {
		t.Fatal(err)
	}
	b.poll()
	if got := *q.fake.torrent(qbHashA).UpLimit; got != 32*1024 {
		t.Fatalf("after the floor went to 32 KiB/s: up_limit %d", got)
	}

	q.settings.UploadFloorKiB = 8
	b.SetUploadDisabled(false)
	if _, err := b.Ensure(q.savePath); err != nil {
		t.Fatal(err)
	}
	b.poll()
	if got := *q.fake.torrent(qbHashA).UpLimit; got != 0 {
		t.Fatalf("disable_seeding off with the floor changed to 8 KiB/s: up_limit %d, want the old 32 KiB/s floor lifted", got)
	}
}

// With the app managing qBittorrent's settings, max_concurrent_downloads is qBittorrent's download
// queue, seeding and active torrents uncapped; 0 is no limit. It is set on connect, on a change
// (the next poll), and again on the next pass when someone changed it in qBittorrent.
func TestQBit_ManagedQueueFollowsMaxConcurrentDownloads(t *testing.T) {
	q := newQBitTest(t)
	b := q.backend
	b.SetMaxActiveDownloads(2)
	q.settings.ManageSettings = true
	n := len(q.fake.callLog())
	if _, err := b.Ensure(q.savePath); err != nil {
		t.Fatal(err)
	}
	want := qbitManagedPrefs{qbitQueuePrefs: qbitQueuePrefs{QueueingEnabled: true, MaxActiveDownloads: 2, MaxActiveUploads: -1, MaxActiveTorrents: -1},
		IncompleteFilesExt: false, DiskIOType: qbitDiskIOSimplePread}
	if q.fake.prefs != want {
		t.Fatalf("after connecting: prefs %+v (calls %v), want %+v", q.fake.prefs, q.callsSince(n), want)
	}

	n = len(q.fake.callLog())
	if _, err := b.Ensure(q.savePath); err != nil {
		t.Fatal(err)
	}
	b.poll()
	if got := q.callsSince(n); len(got) != 0 {
		t.Fatalf("a pass and a poll with the queue already set called %v", got)
	}

	q.fake.mu.Lock()
	q.fake.prefs.MaxActiveDownloads = 7
	q.fake.mu.Unlock()
	n = len(q.fake.callLog())
	if _, err := b.Ensure(q.savePath); err != nil {
		t.Fatal(err)
	}
	if got := q.callsSince(n); !reflect.DeepEqual(got, []string{`setPreferences {"max_active_downloads":2}`}) {
		t.Fatalf("calls after the queue changed in qBittorrent = %v, want only that key sent", got)
	}
	if q.fake.prefs.MaxActiveDownloads != 2 {
		t.Fatalf("a queue changed in qBittorrent stayed at %d after the next pass, want 2", q.fake.prefs.MaxActiveDownloads)
	}

	b.SetMaxActiveDownloads(0)
	b.poll()
	if q.fake.prefs.MaxActiveDownloads != -1 || !q.fake.prefs.QueueingEnabled {
		t.Fatalf("max_concurrent_downloads 0: prefs %+v, want queueing on with no download limit", q.fake.prefs)
	}
	n = len(q.fake.callLog())
	b.SetMaxActiveDownloads(0)
	b.poll()
	if got := q.callsSince(n); len(got) != 0 {
		t.Fatalf("the same value again called %v", got)
	}
}

// Off (the default), the app changes nothing global in qBittorrent, which may be shared.
func TestQBit_UnmanagedQueueIsLeftAlone(t *testing.T) {
	q := newQBitTest(t)
	q.backend.SetMaxActiveDownloads(1)
	q.backend.poll()
	if _, err := q.backend.Ensure(q.savePath); err != nil {
		t.Fatal(err)
	}
	for _, call := range q.fake.callLog() {
		if strings.HasPrefix(call, "setPreferences") {
			t.Fatalf("an unmanaged qBittorrent got %s", call)
		}
	}
	if q.fake.prefs.MaxActiveDownloads != 3 {
		t.Fatalf("qBittorrent's own queue changed to %d", q.fake.prefs.MaxActiveDownloads)
	}
}

// stagedPlacement registers a single-episode placement that stages in a fresh incomplete folder.
func (q *qbTest) stagedPlacement(t *testing.T, hash string) string {
	t.Helper()
	staging := t.TempDir()
	if err := q.backend.SetPlacement(PlacementRequest{Hash: hash, SeriesDir: "Frieren", Kind: PlacementEpisode, Season: 1,
		RenameVideo: "Frieren - S01E05", PartFiles: true, IncompleteRoot: staging}); err != nil {
		t.Fatal(err)
	}
	return staging
}

// incomplete_path with qBittorrent: the torrent downloads in its staging folder as qBittorrent's
// download path, under its final name (no .part: staging is out of Jellyfin's sight), and
// qBittorrent moves it into the library at the end. Finishing waits for the move and then drops the
// download path (decisions/141, decisions/145).
func TestQBit_StagedTorrentDownloadsInTheIncompleteFolderAndMovesIn(t *testing.T) {
	q := newQBitTest(t)
	b := q.backend
	staging := q.stagedPlacement(t, qbHashA)
	var completed []string
	b.SetCallbacks(func(hash string) { completed = append(completed, hash) }, nil)
	if _, err := b.Add(qbMagnet(qbHashA)); err != nil {
		t.Fatal(err)
	}
	stagingDir := filepath.Join(staging, qbHashA)
	want := "add " + qbHashA + " savepath=" + q.root + " stopAtMeta=true useDownloadPath=true limits=-1/-1/-1 upLimit= downloadPath=" + stagingDir
	if !fakeContains(q.fake.callLog(), want) {
		t.Fatalf("calls = %v, want %q", q.fake.callLog(), want)
	}
	q.fake.deliverMetadata(qbHashA, qbitFile{Index: 0, Name: "[Group] Frieren - 05.mkv", Size: 8})
	b.poll()
	if !fakeContains(q.fake.callLog(), "rename "+qbHashA+" [Group] Frieren - 05.mkv -> Frieren/Season 01/Frieren - S01E05.mkv") {
		t.Fatalf("calls = %v, want the final name without .part", q.fake.callLog())
	}
	if info, _ := b.Get(qbHashA); !info.Staged {
		t.Fatal("a downloading staged torrent is not marked Staged")
	}

	final := filepath.Join(q.root, "Frieren", "Season 01", "Frieren - S01E05.mkv")
	q.fake.finish(qbHashA)
	if !hasSize(filepath.Join(stagingDir, "Frieren", "Season 01", "Frieren - S01E05.mkv"), 8) {
		t.Fatal("the file was not downloaded in the incomplete folder")
	}
	b.poll()
	info, _ := b.Get(qbHashA)
	if info.Status != StatusMoving || info.MoveProgress != 0 || len(completed) != 0 {
		t.Fatalf("while qBittorrent moves it: status %q, progress %v, completions %v", info.Status, info.MoveProgress, completed)
	}
	if err := os.MkdirAll(filepath.Dir(final), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final, make([]byte, 2), 0644); err != nil {
		t.Fatal(err)
	}
	if info, _ := b.Get(qbHashA); info.MoveProgress != 0.25 {
		t.Fatalf("2 of 8 bytes at the final path: MoveProgress %v, want 0.25", info.MoveProgress)
	}
	if err := b.FinalizePlacement(qbHashA); err == nil {
		t.Fatal("FinalizePlacement accepted a torrent qBittorrent is still moving")
	}
	// Reported finished before the move starts: the files are not in the library yet.
	q.fake.setState(qbHashA, "stalledUP")
	b.poll()
	b.moveWait = 50 * time.Millisecond
	if err := b.FinalizePlacement(qbHashA); err == nil {
		t.Fatal("FinalizePlacement accepted a finished torrent whose files are still in the incomplete folder")
	}
	if p, _ := b.Placement(qbHashA); p.Completed {
		t.Fatal("the placement was marked completed before the move")
	}
	q.fake.setState(qbHashA, "moving")
	b.poll()

	q.fake.moveIn(qbHashA)
	b.poll()
	if !reflect.DeepEqual(completed, []string{qbHashA}) {
		t.Fatalf("completions after the move = %v, want one", completed)
	}
	n := len(q.fake.callLog())
	if err := b.FinalizePlacement(qbHashA); err != nil {
		t.Fatalf("FinalizePlacement after the move: %v", err)
	}
	if got := q.callsSince(n); !reflect.DeepEqual(got, []string{"setDownloadPath " + qbHashA + " "}) {
		t.Fatalf("finalize calls = %v, want the download path cleared", got)
	}
	p, _ := b.Placement(qbHashA)
	if !p.Completed || !hasSize(final, 8) {
		t.Fatalf("after finalize: completed %v, final file whole %v", p.Completed, hasSize(final, 8))
	}
	if info, _ := b.Get(qbHashA); info.Staged {
		t.Fatal("a finalized torrent is still marked Staged")
	}
}

// A moved incomplete folder (the user moved it and changed incomplete_path) is followed: the
// placement and qBittorrent's download path point at the new folder, and a recheck finds the data.
func TestQBit_MovedIncompleteFolderIsFollowed(t *testing.T) {
	q := newQBitTest(t)
	b := q.backend
	q.stagedPlacement(t, qbHashA)
	if _, err := b.Add(qbMagnet(qbHashA)); err != nil {
		t.Fatal(err)
	}
	moved := t.TempDir()
	n := len(q.fake.callLog())
	if err := b.MovePlacementStaging(qbHashA, moved); err != nil {
		t.Fatal(err)
	}
	want := []string{"setDownloadPath " + qbHashA + " " + filepath.Join(moved, qbHashA), "recheck " + qbHashA}
	if got := q.callsSince(n); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if p, _ := b.Placement(qbHashA); p.IncompleteRoot != moved {
		t.Fatalf("placement root = %q, want %q", p.IncompleteRoot, moved)
	}
}

// A staged torrent never runs while its incomplete folder is gone (a drive unmounted): qBittorrent
// would create the folder on the system disk (measured). A running one is stopped, one whose
// metadata arrives then is renamed but not started, one qBittorrent fails reports
// ErrIncompleteFolderUnavailable (the daemon waits instead of dropping it), and one the user had
// stopped stays stopped. Once the folder is back, the others are rechecked and started
// (decisions/141, decisions/145).
func TestQBit_StagedTorrentWaitsForItsFolderAndRestarts(t *testing.T) {
	q := newQBitTest(t)
	b := q.backend
	staging := q.stagedPlacement(t, qbHashA)
	for _, h := range []string{qbHashB, qbHashC} {
		if err := b.SetPlacement(PlacementRequest{Hash: h, SeriesDir: "Show " + h[:1], Kind: PlacementEpisode, Season: 1,
			IncompleteRoot: staging}); err != nil {
			t.Fatal(err)
		}
	}
	var failures []error
	b.SetCallbacks(nil, func(hash string, err error) { failures = append(failures, err) })
	for _, h := range []string{qbHashA, qbHashB, qbHashC} {
		if _, err := b.Add(qbMagnet(h)); err != nil {
			t.Fatal(err)
		}
	}
	q.fake.deliverMetadata(qbHashA, qbitFile{Index: 0, Name: "a.mkv", Size: 4})
	q.fake.deliverMetadata(qbHashC, qbitFile{Index: 0, Name: "c.mkv", Size: 4})
	b.poll() // A and C renamed and started; B still waits for its metadata
	if err := b.Pause(qbHashC); err != nil {
		t.Fatal(err)
	}

	hidden := staging + ".unmounted"
	if err := os.Rename(staging, hidden); err != nil {
		t.Fatal(err)
	}
	n := len(q.fake.callLog())
	b.poll()
	if got := q.callsSince(n); !reflect.DeepEqual(got, []string{"stop " + qbHashA}) {
		t.Fatalf("calls with the folder gone = %v, want the running download stopped", got)
	}
	q.fake.deliverMetadata(qbHashB, qbitFile{Index: 0, Name: "b.mkv", Size: 4})
	n = len(q.fake.callLog())
	b.poll()
	if got := q.callsSince(n); len(got) != 1 || !strings.HasPrefix(got[0], "rename "+qbHashB) {
		t.Fatalf("metadata arriving with the folder gone: calls %v, want the rename and no start", got)
	}
	q.fake.setState(qbHashA, "error")
	b.poll()
	if len(failures) != 1 || !errors.Is(failures[0], ErrIncompleteFolderUnavailable) {
		t.Fatalf("failures = %v, want ErrIncompleteFolderUnavailable", failures)
	}

	if err := os.Rename(hidden, staging); err != nil {
		t.Fatal(err)
	}
	n = len(q.fake.callLog())
	b.poll()
	want := []string{"recheck " + qbHashA, "start " + qbHashA, "recheck " + qbHashB, "start " + qbHashB}
	if got := q.callsSince(n); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls once the folder is back = %v, want %v (C, paused by the user, stays stopped)", got, want)
	}
}

// A torrent in a download root whose drive is gone never runs there: qBittorrent would create the
// folder on the system disk. A running one is stopped, one qBittorrent fails reports
// ErrDownloadRootUnavailable (the daemon waits), and once the folder is back it is rechecked and
// started (decisions/154).
func TestQBit_TorrentWaitsForItsDownloadRootAndRestarts(t *testing.T) {
	q := newQBitTest(t)
	b := q.backend
	root := filepath.Join(t.TempDir(), "hdd")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := b.SetPlacement(PlacementRequest{Hash: qbHashA, SeriesDir: "Show", Kind: PlacementEpisode, Season: 1, RootPath: root, RootName: "hdd"}); err != nil {
		t.Fatal(err)
	}
	var failures []error
	b.SetCallbacks(nil, func(hash string, err error) { failures = append(failures, err) })
	if _, err := b.Add(qbMagnet(qbHashA)); err != nil {
		t.Fatal(err)
	}
	q.fake.deliverMetadata(qbHashA, qbitFile{Index: 0, Name: "a.mkv", Size: 4})
	b.poll()
	if p, _ := b.Placement(qbHashA); !strings.HasPrefix(p.Files[0].Path, root) {
		t.Fatalf("placed at %s, want under its root %s", p.Files[0].Path, root)
	}

	hidden := root + ".unmounted"
	if err := os.Rename(root, hidden); err != nil {
		t.Fatal(err)
	}
	n := len(q.fake.callLog())
	b.poll()
	if got := q.callsSince(n); !reflect.DeepEqual(got, []string{"stop " + qbHashA}) {
		t.Fatalf("calls with the root gone = %v, want the download stopped", got)
	}
	q.fake.setState(qbHashA, "error")
	b.poll()
	if len(failures) != 1 || !errors.Is(failures[0], ErrDownloadRootUnavailable) {
		t.Fatalf("failures = %v, want ErrDownloadRootUnavailable", failures)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("the missing root must not be created")
	}

	if err := os.Rename(hidden, root); err != nil {
		t.Fatal(err)
	}
	n = len(q.fake.callLog())
	b.poll()
	if got := q.callsSince(n); !reflect.DeepEqual(got, []string{"recheck " + qbHashA, "start " + qbHashA}) {
		t.Fatalf("calls once the root is back = %v, want a recheck and a start", got)
	}
}
