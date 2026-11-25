package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/shoko"
	"AutoAnimeDownloader/src/internal/torrents"
)

// shokoOnConfig is direct mode with the library as the one download folder and Shoko on. Whether
// the library is one of Shoko's import folders is the importer's answer (decisions/152).
func shokoOnConfig(lib, shokoURL string) *files.Config {
	c := directConfig(lib)
	c.ShokoEnabled, c.ShokoURL, c.ShokoAPIKey = true, shokoURL, "key"
	return c
}

// countingImporter counts the import requests; roots are the download folders it reports as
// Shoko's import folders.
type countingImporter struct {
	mu    sync.Mutex
	calls int
	roots map[string]bool
}

func (c *countingImporter) InShoko(_ *files.Config, root string) bool {
	return c.roots[filepath.Clean(root)]
}

func (c *countingImporter) RequestImport(*files.Config) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
}

// placedCompleted registers a direct-mode torrent for episode 5 of anime 3 whose .part file rain
// finished writing, and returns the file's final path.
func placedCompleted(t *testing.T, configs *files.Config, backend *torrents.FakeBackend) string {
	t.Helper()
	if err := registerPlacement(configs, backend, directMagnet, torrentTarget{anime: titledAnime(3, "Show", anilist.MediaFormatTV), episode: 5}, files.DownloadRoot{}); err != nil {
		t.Fatal(err)
	}
	p, err := backend.PlaceFiles(directHash, []string{"ep05.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.Files[0].Path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Files[0].Path+".part", []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	backend.AddCompleted(directHash, p.SeriesDir)
	return p.Files[0].Path
}

// The library in one of Shoko's import folders: once organized, the torrent keeps seeding, its
// record is marked handed to Shoko, and the job queue asks Shoko to import once for the tick. Not
// in one, nothing is handed off and Shoko is not asked (decisions/152).
func TestOrganize_ShokoFolderHandsTheTorrentToShoko(t *testing.T) {
	for _, drop := range []bool{true, false} {
		lib := t.TempDir()
		configs := shokoOnConfig(lib, "http://shoko:8111")
		backend := torrents.NewFakeBackend()
		backend.LibraryRoot = lib
		final := placedCompleted(t, configs, backend)

		fm := &orchestrationFM{saved: []files.EpisodeStruct{{AnimeID: 3, EpisodeHash: directHash, AnimeName: "Show", EpisodeNumber: 5}}, configs: configs}
		imp := &countingImporter{roots: map[string]bool{lib: drop}}
		q := NewJobQueue(fm, filepath.Join(t.TempDir(), "jobs.json"))
		q.SetOrchestration(backend, files.NewLibrarian(files.NewOSFileSystem()))
		q.SetShoko(imp)
		q.EnqueueOrganize(directHash)
		q.processDueJobs()

		rec := fm.saved[0]
		if _, inClient := backend.Get(directHash); !inClient || rec.TorrentReleased {
			t.Errorf("drop=%v: the torrent must keep seeding: inClient=%v record=%+v", drop, inClient, rec)
		}
		if len(rec.LibraryPaths) != 1 || rec.LibraryPaths[0] != final {
			t.Errorf("drop=%v: record must be organized: %+v", drop, rec)
		}
		if rec.HandedToShoko != drop {
			t.Errorf("drop=%v: handed_to_shoko = %v", drop, rec.HandedToShoko)
		}
		if want := map[bool]int{true: 1, false: 0}[drop]; imp.calls != want {
			t.Errorf("drop=%v: Shoko asked %d times, want %d", drop, imp.calls, want)
		}
	}
}

// With disable_seeding on, organize releases the torrent it just handed to Shoko: Shoko is still
// asked to import, since nothing else would ask it (decisions/152).
func TestOrganize_DisableSeedingStillAsksShokoToImport(t *testing.T) {
	lib := t.TempDir()
	configs := shokoOnConfig(lib, "http://shoko:8111")
	configs.DisableSeeding = true
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	placedCompleted(t, configs, backend)

	fm := &orchestrationFM{saved: []files.EpisodeStruct{{AnimeID: 3, EpisodeHash: directHash, AnimeName: "Show", EpisodeNumber: 5}}, configs: configs}
	imp := &countingImporter{roots: map[string]bool{lib: true}}
	q := NewJobQueue(fm, filepath.Join(t.TempDir(), "jobs.json"))
	q.SetOrchestration(backend, files.NewLibrarian(files.NewOSFileSystem()))
	q.SetShoko(imp)
	q.EnqueueOrganize(directHash)
	q.processDueJobs()

	if _, inClient := backend.Get(directHash); inClient || !fm.saved[0].TorrentReleased {
		t.Fatalf("disable_seeding must release the torrent: inClient=%v record=%+v", inClient, fm.saved[0])
	}
	if !fm.saved[0].HandedToShoko {
		t.Error("the record must be handed to Shoko")
	}
	if imp.calls != 1 {
		t.Errorf("Shoko asked %d times, want 1", imp.calls)
	}
}

// Shoko moved a handed-off torrent's files on import: the torrent leaves the client with its
// records released and its placement forgotten. While Shoko has none of its episodes, a missing
// file is the user's deletion and the torrent stays, as decisions/139 says (decisions/152).
func TestReleaseMovedByShoko(t *testing.T) {
	for _, imported := range []bool{true, false} {
		lib := t.TempDir()
		configs := shokoOnConfig(lib, "http://shoko:8111")
		backend := torrents.NewFakeBackend()
		backend.LibraryRoot = lib
		final := placedCompleted(t, configs, backend)
		if err := backend.FinalizePlacement(directHash); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(final); err != nil { // Shoko moved it
			t.Fatal(err)
		}
		fm := &orchestrationFM{saved: []files.EpisodeStruct{{AnimeID: 3, EpisodeHash: directHash, EpisodeNumber: 5, LibraryPaths: []string{final}, HandedToShoko: true}}, configs: configs}
		inShoko := map[int]map[int]shoko.File{3: {}}
		if imported {
			inShoko[3][5] = shoko.File{ID: 9, Path: "Show/Show - 05.mkv"}
		}
		releaseMovedByShoko(backend, fm, fm.saved, inShoko)

		_, inClient := backend.Get(directHash)
		_, placed := backend.Placement(directHash)
		if imported {
			if inClient || !fm.saved[0].TorrentReleased || !fm.saved[0].HandedToShoko || placed {
				t.Errorf("Shoko has it: inClient=%v placed=%v record=%+v; want released, placement forgotten", inClient, placed, fm.saved[0])
			}
			if keep, removed := backend.RemovedKeepData[directHash]; !removed || !keep {
				t.Errorf("removed=%v keepData=%v; want a removal keeping the files", removed, keep)
			}
		} else if !inClient || fm.saved[0].TorrentReleased || !placed {
			t.Errorf("Shoko doesn't have it: inClient=%v placed=%v record=%+v; want it left as it is", inClient, placed, fm.saved[0])
		}
	}
}

// fakeShokoImports answers Shoko's import action, counting the requests, with status (200 unless
// set) and the import folders.
type fakeShokoImports struct {
	mu      sync.Mutex
	imports int
	folders int
	status  int
}

func (f *fakeShokoImports) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/api/v3/Action/ImportNewFiles":
			f.imports++
			if f.status != 0 {
				w.WriteHeader(f.status)
			}
		case "/api/v3/ImportFolder":
			f.folders++
			_, _ = w.Write([]byte(`[{"ID":1,"Name":"drop","Path":"/import/","DropFolderType":"Source","WatchForNewFiles":false}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func (f *fakeShokoImports) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.imports, f.folders
}

// Downloads that finish close together share one import request; a refused one is shown and sent
// again by the next pass; nothing is asked with Shoko off or in hardlink mode (decisions/152).
func TestRequestImport(t *testing.T) {
	shk := &fakeShokoImports{}
	srv := httptest.NewServer(shk.handler())
	defer srv.Close()
	svc := NewShokoService(t.TempDir())
	svc.importSettle = 30 * time.Millisecond
	cfg := shokoOnConfig(t.TempDir(), srv.URL)

	for range 3 {
		svc.RequestImport(cfg)
	}
	waitFor(t, "the import request", func() bool { n, _ := shk.counts(); return n == 1 })
	waitFor(t, "the sender to stop", func() bool { svc.mu.Lock(); defer svc.mu.Unlock(); return !svc.importRunning })
	if n, _ := shk.counts(); n != 1 {
		t.Fatalf("three finished downloads made %d requests, want 1", n)
	}
	if st := svc.ImportState(); st.LastRequest.IsZero() || st.Error != "" {
		t.Fatalf("after a good request: %+v", st)
	}

	shk.mu.Lock()
	shk.status = http.StatusForbidden
	shk.mu.Unlock()
	svc.RequestImport(cfg)
	waitFor(t, "the refusal", func() bool { return svc.ImportState().Error != "" })
	waitFor(t, "the sender to stop", func() bool { svc.mu.Lock(); defer svc.mu.Unlock(); return !svc.importRunning })

	shk.mu.Lock()
	shk.status = 0
	shk.mu.Unlock()
	svc.retryImport(cfg)
	waitFor(t, "the retry", func() bool { n, _ := shk.counts(); return n == 3 })
	waitFor(t, "the error to clear", func() bool { return svc.ImportState().Error == "" })
	waitFor(t, "the sender to stop", func() bool { svc.mu.Lock(); defer svc.mu.Unlock(); return !svc.importRunning })
	svc.retryImport(cfg)
	time.Sleep(80 * time.Millisecond)
	if n, _ := shk.counts(); n != 3 {
		t.Fatalf("a pass after a good request asked again: %d requests, want 3", n)
	}

	off := shokoOnConfig(t.TempDir(), srv.URL)
	off.ShokoEnabled = false
	svc.RequestImport(off)
	hardlink := shokoOnConfig(t.TempDir(), srv.URL)
	hardlink.LibraryMode = files.LibraryModeHardlink
	svc.RequestImport(hardlink)
	time.Sleep(80 * time.Millisecond)
	if n, _ := shk.counts(); n != 3 {
		t.Fatalf("Shoko was asked while off or in hardlink mode: %d requests, want 3", n)
	}
	var nilService *ShokoService
	nilService.RequestImport(cfg) // the debug run and tests have none
}

// Shoko's import folders are read once, then again only with a sync; a failure keeps the last list.
func TestReadImportFolders(t *testing.T) {
	shk := &fakeShokoImports{}
	srv := httptest.NewServer(shk.handler())
	svc := NewShokoService(t.TempDir())
	cfg := shokoOnConfig(t.TempDir(), srv.URL)
	ctx := t.Context()

	svc.readFolders(ctx, cfg, false)
	svc.readFolders(ctx, cfg, false)
	if _, n := shk.counts(); n != 1 {
		t.Fatalf("read %d times without a sync, want 1", n)
	}
	st := svc.ImportState()
	if len(st.Folders) != 1 || !st.Folders[0].IsDropSource() || st.Folders[0].Path != "/import/" {
		t.Fatalf("folders = %+v", st.Folders)
	}
	srv.Close()
	svc.readFolders(ctx, cfg, true)
	if st := svc.ImportState(); len(st.Folders) != 1 || st.FoldersError == "" {
		t.Fatalf("a failed read must keep the list and say why: %+v", st)
	}
}

// Deleting an episode handed to Shoko removes its record and placement and never its file: Shoko
// imports where the file lies unless its renamer moves it, so the file in Shoko's folder is
// Shoko's. A released torrent that wasn't handed off still loses its files (decisions/137).
func TestRemoveEpisodes_HandedToShokoKeepsTheFile(t *testing.T) {
	for _, handed := range []bool{true, false} {
		lib := t.TempDir()
		configs := shokoOnConfig(lib, "http://shoko:8111")
		backend := torrents.NewFakeBackend()
		backend.LibraryRoot = lib
		if err := registerPlacement(configs, backend, directMagnet, torrentTarget{anime: titledAnime(3, "Show", anilist.MediaFormatTV), episode: 1}, files.DownloadRoot{}); err != nil {
			t.Fatal(err)
		}
		p, err := backend.PlaceFiles(directHash, []string{"ep01.mkv"})
		if err != nil {
			t.Fatal(err)
		}
		final := p.Files[0].Path
		if err := os.MkdirAll(filepath.Dir(final), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(final, []byte("video"), 0644); err != nil {
			t.Fatal(err)
		}
		saved := []files.EpisodeStruct{{AnimeID: 3, EpisodeNumber: 1, EpisodeHash: directHash, LibraryPaths: []string{final}, TorrentReleased: true, HandedToShoko: handed}}
		fm := &orchestrationFM{saved: saved, configs: configs}
		if err := removeEpisodesAndLinks(fm, backend, &spyLibrarian{}, []files.EpisodeKey{{AnimeID: 3, Episode: 1}}, saved, false, false, map[int]bool{}); err != nil {
			t.Fatal(err)
		}
		_, statErr := os.Stat(final)
		if handed && statErr != nil {
			t.Errorf("a file handed to Shoko must stay: %v", statErr)
		}
		if !handed && !os.IsNotExist(statErr) {
			t.Errorf("a released file the app still owns must go: %v", statErr)
		}
		if _, ok := backend.Placement(directHash); ok {
			t.Errorf("handed=%v: the placement must be forgotten, or the orphan sweep deletes the files later", handed)
		}
		if len(fm.deleted) != 1 {
			t.Errorf("handed=%v: the record must be deleted, got %v", handed, fm.deleted)
		}
	}
}

// A whole pack handed to Shoko keeps every file: once the first record forgets the placement, the
// next record of the same torrent must not fall through to the library removal. Neither may a
// torrent with no placement (organized by hardlink before the switch to direct mode and handed off
// by reconcileLibrary), whose library file is Shoko's too.
func TestRemoveEpisodes_HandedToShokoPackKeepsEveryFile(t *testing.T) {
	lib := t.TempDir()
	configs := shokoOnConfig(lib, "http://shoko:8111")
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	if err := registerPlacement(configs, backend, directMagnet, torrentTarget{anime: titledAnime(3, "Show", anilist.MediaFormatTV), batch: true}, files.DownloadRoot{}); err != nil {
		t.Fatal(err)
	}
	p, err := backend.PlaceFiles(directHash, []string{"Show/ep01.mkv", "Show/ep02.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range p.Files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.Path, []byte("video"), 0644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, f.Path)
	}
	// The hardlink-era torrent: no placement, its library file in Shoko's folder.
	const linked = "3333333333333333333333333333333333333333"
	linkedFile := filepath.Join(lib, "Other", "ep01.mkv")
	if err := os.MkdirAll(filepath.Dir(linkedFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(linkedFile, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}

	saved := []files.EpisodeStruct{
		{AnimeID: 3, EpisodeNumber: 1, EpisodeHash: directHash, IsBatch: true, LibraryPaths: paths, TorrentReleased: true, HandedToShoko: true},
		{AnimeID: 3, EpisodeNumber: 2, EpisodeHash: directHash, IsBatch: true, LibraryPaths: paths, TorrentReleased: true, HandedToShoko: true},
		{AnimeID: 4, EpisodeNumber: 1, EpisodeHash: linked, LibraryPaths: []string{linkedFile}, TorrentReleased: true, HandedToShoko: true},
	}
	fm := &orchestrationFM{saved: saved, configs: configs}
	keys := []files.EpisodeKey{{AnimeID: 3, Episode: 1}, {AnimeID: 3, Episode: 2}, {AnimeID: 4, Episode: 1}}
	if err := removeEpisodesAndLinks(fm, backend, files.NewLibrarian(files.NewOSFileSystem()), keys, saved, false, false, map[int]bool{}); err != nil {
		t.Fatal(err)
	}
	for _, f := range append(paths, linkedFile) {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s was handed to Shoko and must stay: %v", f, err)
		}
	}
	if _, ok := backend.Placement(directHash); ok {
		t.Error("the pack's placement must be forgotten with its last record")
	}
	if len(fm.deleted) != 3 {
		t.Errorf("every record must be deleted, got %v", fm.deleted)
	}
}

// Deleting an episode whose handed-off torrent still seeds removes the torrent with its files kept
// and its placement Kept, so the orphan sweep never deletes Shoko's files (decisions/152).
func TestRemoveEpisodes_HandedToShokoSeedingKeepsTheFiles(t *testing.T) {
	lib := t.TempDir()
	configs := shokoOnConfig(lib, "http://shoko:8111")
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	final := placedCompleted(t, configs, backend)
	if err := backend.FinalizePlacement(directHash); err != nil {
		t.Fatal(err)
	}
	saved := []files.EpisodeStruct{{AnimeID: 3, EpisodeNumber: 5, EpisodeHash: directHash, LibraryPaths: []string{final}, HandedToShoko: true}}
	fm := &orchestrationFM{saved: saved, configs: configs}
	if err := removeEpisodesAndLinks(fm, backend, &spyLibrarian{}, []files.EpisodeKey{{AnimeID: 3, Episode: 5}}, saved, false, false, map[int]bool{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(final); err != nil {
		t.Errorf("Shoko's file must stay: %v", err)
	}
	if keep, removed := backend.RemovedKeepData[directHash]; !removed || !keep {
		t.Errorf("removed=%v keepData=%v; want the torrent removed with its files kept", removed, keep)
	}
	if p, ok := backend.Placement(directHash); !ok || !p.Kept {
		t.Errorf("the placement must stay, Kept, or the orphan sweep deletes the files: %+v, %v", p, ok)
	}
	if len(fm.deleted) != 1 {
		t.Errorf("the record must be deleted, got %v", fm.deleted)
	}
}

// Each download folder is in Shoko or not: a torrent organized in Shoko's folder is handed to
// Shoko, one in another folder is an ordinary download (decisions/152, decisions/154).
func TestOrganize_OnlyTheShokoFolderHandsOff(t *testing.T) {
	lib := t.TempDir()
	shokoDir, otherDir := t.TempDir(), t.TempDir()
	configs := directConfig(lib)
	configs.ShokoEnabled, configs.ShokoURL, configs.ShokoAPIKey = true, "http://shoko:8111", "key"
	configs.DownloadRoots = []files.DownloadRoot{{Name: "shoko", Path: shokoDir}, {Name: "other", Path: otherDir}}
	for _, tc := range []struct {
		root   files.DownloadRoot
		handed bool
	}{{configs.DownloadRoots[0], true}, {configs.DownloadRoots[1], false}} {
		backend := torrents.NewFakeBackend()
		backend.LibraryRoot = lib
		if err := registerPlacement(configs, backend, directMagnet, torrentTarget{anime: titledAnime(3, "Show", anilist.MediaFormatTV), episode: 5}, tc.root); err != nil {
			t.Fatal(err)
		}
		p, err := backend.PlaceFiles(directHash, []string{"ep05.mkv"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(p.Files[0].Path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p.Files[0].Path+".part", []byte("video"), 0644); err != nil {
			t.Fatal(err)
		}
		backend.AddCompleted(directHash, p.SeriesDir)
		fm := &orchestrationFM{saved: []files.EpisodeStruct{{AnimeID: 3, EpisodeHash: directHash, AnimeName: "Show", EpisodeNumber: 5}}, configs: configs}
		imp := &countingImporter{roots: map[string]bool{shokoDir: true}}
		q := NewJobQueue(fm, filepath.Join(t.TempDir(), "jobs.json"))
		q.SetOrchestration(backend, files.NewLibrarian(files.NewOSFileSystem()))
		q.SetShoko(imp)
		q.EnqueueOrganize(directHash)
		q.processDueJobs()
		if len(fm.saved[0].LibraryPaths) != 1 {
			t.Fatalf("root %s: record must be organized: %+v", tc.root.Name, fm.saved[0])
		}
		if got := fm.saved[0].HandedToShoko; got != tc.handed {
			t.Errorf("root %s: handed_to_shoko = %v, want %v", tc.root.Name, got, tc.handed)
		}
		// Another folder has the switch, this one doesn't: Shoko is not asked.
		if want := map[bool]int{true: 1, false: 0}[tc.handed]; imp.calls != want {
			t.Errorf("root %s: Shoko asked %d times, want %d", tc.root.Name, imp.calls, want)
		}
	}
}

// A download folder is Shoko's when, as Shoko sees it through shoko_path_map, it is one of Shoko's
// import folders or lies inside one; the deepest folder wins. Read from the folders as last read,
// only with Shoko on and in direct mode (decisions/152).
func TestShokoFolder(t *testing.T) {
	svc := NewShokoService(t.TempDir())
	if err := svc.store.SetImportFolders([]shoko.ImportFolder{
		{ID: 1, Name: "drop", Path: "/import/", DropType: "Source"},
		{ID: 2, Name: "anime", Path: "/anime/", DropType: "Destination"},
		{ID: 3, Name: "seasonal", Path: "/anime/seasonal/", DropType: "None"},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := shokoOnConfig("/mnt/lib", "http://shoko:8111")
	cfg.ShokoPathMap = []files.ShokoPathMapping{{App: "/mnt/shoko-drop", Shoko: "/import"}}
	for root, want := range map[string]int{
		"/mnt/shoko-drop":        1,
		"/mnt/shoko-drop/sub":    1,
		"/import":                1, // unmapped: the same path on both sides
		"/anime/seasonal/winter": 3, // the deepest folder
		"/anime":                 2,
		"/mnt/lib":               0,
		"/mnt/shoko-dropped":     0,
	} {
		f, ok := svc.ShokoFolder(cfg, root)
		if got := map[bool]int{true: f.ID, false: 0}[ok]; got != want {
			t.Errorf("ShokoFolder(%q) = folder %d, want %d", root, got, want)
		}
	}
	off := *cfg
	off.ShokoEnabled = false
	hardlink := *cfg
	hardlink.LibraryMode = files.LibraryModeHardlink
	var nilService *ShokoService
	if svc.InShoko(&off, "/mnt/shoko-drop") || svc.InShoko(&hardlink, "/mnt/shoko-drop") || nilService.InShoko(cfg, "/mnt/shoko-drop") {
		t.Error("Shoko off, hardlink mode or no service: no folder is Shoko's")
	}
}
