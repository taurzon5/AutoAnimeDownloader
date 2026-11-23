package torrents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cenkalti/rain/v2/storage"
)

const testHash = "0123456789abcdef0123456789abcdef01234567"

func noFile(string) bool { return false }

func plan(t *testing.T, req PlacementRequest, paths ...string) []PlacedFile {
	t.Helper()
	files, err := planFiles(req, paths, noFile, noFile)
	if err != nil {
		t.Fatalf("planFiles: %v", err)
	}
	return files
}

func TestPlanFiles_EpisodeGoesToSeasonFolder(t *testing.T) {
	req := PlacementRequest{Hash: testHash, SeriesDir: "Frieren", Kind: PlacementEpisode, Season: 1}
	files := plan(t, req, "[SubsPlease] Frieren - 05 (1080p).mkv")
	if got, want := files[0].Path, "Frieren/Season 01/[SubsPlease] Frieren - 05 (1080p).mkv"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPlanFiles_EpisodeRenameOnlyTouchesTheSingleVideo(t *testing.T) {
	req := PlacementRequest{Hash: testHash, SeriesDir: "Frieren", Kind: PlacementEpisode, Season: 2, RenameVideo: "Frieren - S02E05"}
	files := plan(t, req, "Release/ep05.mkv", "Release/ep05.ass")
	if got, want := files[0].Path, "Frieren/Season 02/Frieren - S02E05.mkv"; got != want {
		t.Fatalf("video: got %q, want %q", got, want)
	}
	if got, want := files[1].Path, "Frieren/Season 02/ep05.ass"; got != want {
		t.Fatalf("subtitle keeps its name: got %q, want %q", got, want)
	}
}

func TestPlanFiles_EpisodeWithTwoVideosKeepsRawNames(t *testing.T) {
	req := PlacementRequest{Hash: testHash, SeriesDir: "S", Kind: PlacementEpisode, Season: 1, RenameVideo: "S - S01E01"}
	files := plan(t, req, "R/ep01.mkv", "R/NCOP.mkv")
	if filepath.Base(files[0].Path) != "ep01.mkv" || filepath.Base(files[1].Path) != "NCOP.mkv" {
		t.Fatalf("two videos must keep raw names, got %v", files)
	}
}

func TestPlanFiles_PackKeepsItsOwnLayout(t *testing.T) {
	req := PlacementRequest{Hash: testHash, SeriesDir: "Kaguya", Kind: PlacementPack}
	files := plan(t, req, "[EMBER] Kaguya S1+S2/Season 1/S01E01.mkv", "[EMBER] Kaguya S1+S2/Season 2/S02E01.mkv")
	if got, want := files[0].Path, "Kaguya/[EMBER] Kaguya S1+S2/Season 1/S01E01.mkv"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got, want := files[1].Path, "Kaguya/[EMBER] Kaguya S1+S2/Season 2/S02E01.mkv"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPlanFiles_MovieGoesToSpecials(t *testing.T) {
	req := PlacementRequest{Hash: testHash, SeriesDir: "Kimetsu", Kind: PlacementMovie}
	files := plan(t, req, "Mugen Train/Mugen Train (2020).mkv")
	if got, want := files[0].Path, "Kimetsu/Specials/Mugen Train (2020).mkv"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPlanFiles_SameFlatNameInOneTorrentStaysUnique(t *testing.T) {
	req := PlacementRequest{Hash: testHash, SeriesDir: "S", Kind: PlacementEpisode, Season: 1}
	files := plan(t, req, "R/a/ep.mkv", "R/b/ep.mkv")
	if files[0].Path == files[1].Path {
		t.Fatalf("two files must not share a destination: %v", files)
	}
}

func TestPlanFiles_ExistingOrClaimedDestinationGetsTheHash(t *testing.T) {
	req := PlacementRequest{Hash: testHash, SeriesDir: "S", Kind: PlacementEpisode, Season: 1}
	exists := func(p string) bool { return p == "S/Season 01/ep.mkv" }
	files, err := planFiles(req, []string{"ep.mkv"}, exists, noFile)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := files[0].Path, "S/Season 01/ep [01234567].mkv"; got != want {
		t.Fatalf("a file already on disk must not be adopted: got %q, want %q", got, want)
	}

	taken := func(p string) bool { return p == "S/Season 01/ep.mkv" }
	files, err = planFiles(req, []string{"ep.mkv"}, noFile, taken)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := files[0].Path, "S/Season 01/ep [01234567].mkv"; got != want {
		t.Fatalf("a path another placement claims must not be shared: got %q, want %q", got, want)
	}
}

func TestPlanFiles_RejectsPathsLeavingTheTorrent(t *testing.T) {
	req := PlacementRequest{Hash: testHash, SeriesDir: "S", Kind: PlacementPack}
	if _, err := planFiles(req, []string{"../../etc/passwd"}, noFile, noFile); err == nil {
		t.Fatal("a path outside the torrent must be rejected")
	}
}

func TestPlacementStore_RegisterKeepsTheFirstPlacement(t *testing.T) {
	s := newPlacementStore("")
	first := PlacementRequest{Hash: testHash, SeriesDir: "A", Kind: PlacementEpisode, Season: 1}
	if _, err := s.register(first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.register(PlacementRequest{Hash: testHash, SeriesDir: "B", Kind: PlacementPack}); err != nil {
		t.Fatal(err)
	}
	p, _ := s.get(testHash)
	if p.SeriesDir != "A" {
		t.Fatalf("the frozen placement must win, got %q", p.SeriesDir)
	}
}

func TestPlacementStore_RejectsBadRequests(t *testing.T) {
	s := newPlacementStore("")
	for _, req := range []PlacementRequest{
		{SeriesDir: "A", Kind: PlacementEpisode},
		{Hash: testHash, SeriesDir: "/abs/olute", Kind: PlacementEpisode},
		{Hash: testHash, SeriesDir: "../escape", Kind: PlacementEpisode},
		{Hash: testHash, SeriesDir: "A", Kind: "nope"},
	} {
		if _, err := s.register(req); err == nil {
			t.Errorf("register(%+v) should fail", req)
		}
	}
}

func TestPlacementStore_PersistsAcrossLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), placementsFileName)
	s := newPlacementStore(path)
	if _, err := s.register(PlacementRequest{Hash: testHash, SeriesDir: "A", Kind: PlacementEpisode, Season: 3}); err != nil {
		t.Fatal(err)
	}
	reloaded := newPlacementStore(path)
	p, ok := reloaded.get(testHash)
	if !ok || p.Season != 3 {
		t.Fatalf("placement must survive a reload, got %+v %v", p, ok)
	}
}

// newPlaced builds a placed storage over a temp library and prepares it with the given files.
func newPlaced(t *testing.T, partFiles bool, paths ...string) (*placementProvider, *placedStorage, string) {
	t.Helper()
	lib := t.TempDir()
	store := newPlacementStore(filepath.Join(t.TempDir(), placementsFileName))
	req := PlacementRequest{Hash: testHash, SeriesDir: "Show", Kind: PlacementEpisode, Season: 1, PartFiles: partFiles}
	if _, err := store.register(req); err != nil {
		t.Fatal(err)
	}
	pp := &placementProvider{store: store, root: lib, fallbackDir: filepath.Join(lib, ".torrents"), perm: 0755}
	sto, err := pp.GetStorage(testHash)
	if err != nil {
		t.Fatal(err)
	}
	ps := sto.(*placedStorage)
	m := storage.Manifest{}
	for _, p := range paths {
		m.Files = append(m.Files, storage.ManifestFile{Path: p, Size: 4})
	}
	if err := ps.Prepare(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	return pp, ps, lib
}

func writeThrough(t *testing.T, ps *placedStorage, name string) {
	t.Helper()
	f, _, err := ps.Open(name, 4)
	if err != nil {
		t.Fatalf("Open(%q): %v", name, err)
	}
	if _, err := f.WriteAt([]byte("data"), 0); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

func TestPlacedStorage_WritesPartThenFinalizeRenames(t *testing.T) {
	_, ps, lib := newPlaced(t, true, "ep.mkv")
	writeThrough(t, ps, "ep.mkv")

	final := filepath.Join(lib, "Show", "Season 01", "ep.mkv")
	if _, err := os.Stat(final + partSuffix); err != nil {
		t.Fatalf("an incomplete file must be written as .part: %v", err)
	}
	if _, err := os.Stat(final); err == nil {
		t.Fatal("the final name must not exist before finalizing")
	}

	p, _ := ps.store.get(testHash)
	if err := finalizeFiles(p.Resolve(lib)); err != nil {
		t.Fatal(err)
	}
	p.Completed = true
	if err := ps.store.update(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(final); err != nil {
		t.Fatalf("finalize must give the file its final name: %v", err)
	}
	// After completion rain reopens by the final name and finds the data.
	f, exists, err := ps.Open("ep.mkv", 4)
	if err != nil || !exists {
		t.Fatalf("reopen after finalize: exists=%v err=%v", exists, err)
	}
	_ = f.Close()
	if err := finalizeFiles(p.Resolve(lib)); err != nil {
		t.Fatalf("finalize must be idempotent: %v", err)
	}
}

// A crash between the rename and saving Completed leaves the final name on disk with the
// placement still incomplete: Open must find the data instead of creating an empty .part.
func TestPlacedStorage_OpenFindsTheFinalNameAfterACrashMidFinalize(t *testing.T) {
	_, ps, lib := newPlaced(t, true, "ep.mkv")
	writeThrough(t, ps, "ep.mkv")
	p, _ := ps.store.get(testHash)
	if err := finalizeFiles(p.Resolve(lib)); err != nil {
		t.Fatal(err)
	}
	f, exists, err := ps.Open("ep.mkv", 4)
	if err != nil || !exists {
		t.Fatalf("Open must use the final name: exists=%v err=%v", exists, err)
	}
	_ = f.Close()
	if _, err := os.Stat(filepath.Join(lib, "Show", "Season 01", "ep.mkv"+partSuffix)); err == nil {
		t.Fatal("Open must not recreate the .part file")
	}
}

func TestPlacedStorage_InPlaceWritesTheFinalName(t *testing.T) {
	_, ps, lib := newPlaced(t, false, "ep.mkv")
	writeThrough(t, ps, "ep.mkv")
	if _, err := os.Stat(filepath.Join(lib, "Show", "Season 01", "ep.mkv")); err != nil {
		t.Fatalf("in_place must write the final name: %v", err)
	}
}

func TestPlacedStorage_OpenRejectsUnmappedNames(t *testing.T) {
	_, ps, _ := newPlaced(t, false, "ep.mkv")
	if _, _, err := ps.Open("other.mkv", 4); err == nil {
		t.Fatal("a name outside the frozen map must be refused")
	}
}

func TestPlacedStorage_PrepareKeepsTheFrozenMap(t *testing.T) {
	_, ps, _ := newPlaced(t, false, "ep.mkv")
	before, _ := ps.store.get(testHash)
	// A second Prepare (rain calls it before every allocation) must not replan.
	if err := ps.Prepare(context.Background(), storage.Manifest{Files: []storage.ManifestFile{{Path: "ep.mkv", Size: 4}}}); err != nil {
		t.Fatal(err)
	}
	after, _ := ps.store.get(testHash)
	if before.Files[0].Path != after.Files[0].Path {
		t.Fatalf("the map moved: %q -> %q", before.Files[0].Path, after.Files[0].Path)
	}
	if err := ps.Prepare(context.Background(), storage.Manifest{Files: []storage.ManifestFile{{Path: "a"}, {Path: "b"}}}); err == nil {
		t.Fatal("a manifest with a different file count must be refused")
	}
}

func TestPlacedStorage_RemoveDeletesFilesAndEmptyFoldersOnly(t *testing.T) {
	_, ps, lib := newPlaced(t, true, "ep.mkv")
	writeThrough(t, ps, "ep.mkv")
	if err := os.WriteFile(filepath.Join(lib, "Show", "tvshow.nfo"), []byte("<tvshow/>"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := ps.Remove(nil, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := ps.store.get(testHash); !ok {
		t.Fatal("keepData must keep the placement: a re-add resumes into the same files")
	}

	if err := ps.Remove(&storage.Manifest{}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lib, "Show")); !os.IsNotExist(err) {
		t.Fatalf("the emptied season and series folders must go (an nfo alone counts as empty): %v", err)
	}
	if _, err := os.Stat(lib); err != nil {
		t.Fatalf("the library root must stay: %v", err)
	}
	if _, ok := ps.store.get(testHash); ok {
		t.Fatal("removing the data must forget the placement")
	}
}

func TestPlacedStorage_RemoveLeavesOtherFilesOfTheSeries(t *testing.T) {
	_, ps, lib := newPlaced(t, false, "ep.mkv")
	writeThrough(t, ps, "ep.mkv")
	other := filepath.Join(lib, "Show", "Season 01", "other.mkv")
	if err := os.WriteFile(other, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ps.Remove(&storage.Manifest{}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("another episode in the folder must survive: %v", err)
	}
}

func TestPlacementProvider_FallsBackToTheLegacyLayout(t *testing.T) {
	pp, _, lib := newPlaced(t, false, "ep.mkv")
	sto, err := pp.GetStorage("ffffffffffffffffffffffffffffffffffffffff")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sto.RootDir(), filepath.Join(lib, ".torrents", "ffffffffffffffffffffffffffffffffffffffff"); got != want {
		t.Fatalf("an unplaced torrent must keep <DataDir>/<id>: got %q, want %q", got, want)
	}
	f, _, err := sto.Open("Show/ep.mkv", 4)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if _, err := os.Stat(filepath.Join(want(lib), "Show", "ep.mkv")); err != nil {
		t.Fatalf("legacy storage must write under <DataDir>/<id>: %v", err)
	}
}

func want(lib string) string {
	return filepath.Join(lib, ".torrents", "ffffffffffffffffffffffffffffffffffffffff")
}

func TestPlacementVideoPaths(t *testing.T) {
	p := Placement{Files: []PlacedFile{{Path: "/l/S/b.mkv"}, {Path: "/l/S/a.ass"}, {Path: "/l/S/a.mkv"}}}
	got := p.VideoPaths()
	if len(got) != 2 || got[0] != "/l/S/a.mkv" || got[1] != "/l/S/b.mkv" {
		t.Fatalf("VideoPaths = %v", got)
	}
}

func TestRainConfig_DirectModeInstallsThePlacementProvider(t *testing.T) {
	cfg := rainConfig("/lib/.torrents", "/data/session.db", sessionOptions{})
	if cfg.CustomStorage != nil {
		t.Fatal("hardlink mode must keep rain's own file storage")
	}
	cfg = rainConfig("/lib/.torrents", "/data/session.db", sessionOptions{placements: newPlacementStore("")})
	pp, ok := cfg.CustomStorage.(*placementProvider)
	if !ok || pp.fallbackDir != "/lib/.torrents" || pp.root != "/lib" {
		t.Fatalf("direct mode must install the placement provider, got %#v", cfg.CustomStorage)
	}
}

// Placements are stored relative to the library: a library the user moves (and re-points the
// config at) is found again under the new root, so rain resumes instead of downloading it all
// again (decisions/034).
func TestPlacedStorage_FollowsAMovedLibrary(t *testing.T) {
	pp, ps, lib := newPlaced(t, false, "ep.mkv")
	writeThrough(t, ps, "ep.mkv")

	moved := filepath.Join(t.TempDir(), "NewDisk")
	if err := os.Rename(lib, moved); err != nil {
		t.Fatal(err)
	}
	stored, _ := pp.store.get(testHash)
	if filepath.IsAbs(stored.SeriesDir) || filepath.IsAbs(stored.Files[0].Path) {
		t.Fatalf("the store must keep relative paths, got %+v", stored)
	}

	moved2 := &placementProvider{store: pp.store, root: moved, fallbackDir: filepath.Join(moved, ".torrents"), perm: 0755}
	sto, err := moved2.GetStorage(testHash)
	if err != nil {
		t.Fatal(err)
	}
	f, exists, err := sto.Open("ep.mkv", 4)
	if err != nil || !exists {
		t.Fatalf("the moved file must be found under the new root: exists=%v err=%v", exists, err)
	}
	_ = f.Close()
	if got, want := sto.RootDir(), filepath.Join(moved, "Show"); got != want {
		t.Fatalf("RootDir must follow the root: got %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(lib, "Show")); !os.IsNotExist(err) {
		t.Fatal("nothing may be recreated at the old location")
	}
}

// Jellyfin leaves an episode .nfo sidecar and a .chapters folder next to a video. Deleting the
// torrent must not leave them keeping Season NN/ and the series folder alive (decisions/106, 110).
func TestPlacedStorage_RemoveClearsJellyfinLeftovers(t *testing.T) {
	_, ps, lib := newPlaced(t, false, "ep.mkv")
	writeThrough(t, ps, "ep.mkv")
	season := filepath.Join(lib, "Show", "Season 01")
	if err := os.WriteFile(filepath.Join(season, "ep.nfo"), []byte("<episode/>"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(season, ".chapters"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(season, ".chapters", "ep.xml"), []byte("<chapters/>"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "Show", "tvshow.nfo"), []byte("<tvshow/>"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := ps.Remove(&storage.Manifest{}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lib, "Show")); !os.IsNotExist(err) {
		t.Fatalf("leftover metadata must not keep the series folder: %v", err)
	}
}

func TestPlacementResolve(t *testing.T) {
	p := Placement{PlacementRequest: PlacementRequest{SeriesDir: "Show"}, Files: []PlacedFile{{Torrent: "a.mkv", Path: "Show/Season 01/a.mkv"}}}
	r := p.Resolve("/lib")
	if r.SeriesDir != "/lib/Show" || r.Files[0].Path != "/lib/Show/Season 01/a.mkv" || r.Files[0].Torrent != "a.mkv" {
		t.Fatalf("Resolve = %+v", r)
	}
	if p.Files[0].Path != "Show/Season 01/a.mkv" {
		t.Fatal("Resolve must not modify the stored placement")
	}
	if same := p.Resolve(""); same.SeriesDir != "Show" {
		t.Fatalf("an empty root leaves paths relative, got %q", same.SeriesDir)
	}
}

// keep_data, then the same torrent comes back (the pass re-downloads the episode): the kept
// placement is reused with its frozen map — the re-add resumes into the files instead of planning
// a [hash] copy. It stays Kept until the daemon's sweep sees the torrent back in the session: the
// add may still fail.
func TestPlacementStore_ReAddedKeptPlacementResumes(t *testing.T) {
	s := newPlacementStore("")
	req := PlacementRequest{Hash: testHash, SeriesDir: "Show", Kind: PlacementEpisode, Season: 1}
	if _, err := s.register(req); err != nil {
		t.Fatal(err)
	}
	p, _ := s.get(testHash)
	p.Files = []PlacedFile{{Torrent: "ep.mkv", Path: "Show/Season 01/ep.mkv"}}
	if err := s.update(p); err != nil {
		t.Fatal(err)
	}
	if err := s.setKept(testHash, true); err != nil {
		t.Fatal(err)
	}
	again, err := s.register(req)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Kept || len(again.Files) != 1 || again.Files[0].Path != "Show/Season 01/ep.mkv" {
		t.Fatalf("the re-add must reuse the kept placement, still kept, with its frozen map; got %+v", again)
	}
}

func TestRemovePlacedFiles_RefusesAnUnresolvedPlacement(t *testing.T) {
	p := Placement{PlacementRequest: PlacementRequest{Hash: testHash, SeriesDir: "Show"}, Files: []PlacedFile{{Path: "Show/Season 01/ep.mkv"}}}
	if err := RemovePlacedFiles(p); err == nil {
		t.Fatal("relative paths would resolve against the working directory; must refuse")
	}
}

// A removal before the metadata arrived (rain passes no manifest) never deletes a Kept placement's
// files — the user's — but still cleans up the daemon's own partial data.
func TestPlacedStorage_RemoveWithoutMetadata(t *testing.T) {
	_, ps, lib := newPlaced(t, false, "ep.mkv")
	writeThrough(t, ps, "ep.mkv")
	file := filepath.Join(lib, "Show", "Season 01", "ep.mkv")

	if err := ps.store.setKept(testHash, true); err != nil {
		t.Fatal(err)
	}
	if err := ps.Remove(nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("a kept placement's files must survive a removal with no metadata: %v", err)
	}
	if p, ok := ps.store.get(testHash); !ok || !p.Kept {
		t.Fatalf("the kept placement must stay, got %+v ok=%v", p, ok)
	}

	if err := ps.store.setKept(testHash, false); err != nil {
		t.Fatal(err)
	}
	if err := ps.Remove(nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the daemon's own data is deleted even without metadata: %v", err)
	}
}

// Registering a hash again keeps its placement but refreshes CreatedAt: the daemon's orphan sweep
// waits out the gap between registration and the add by that timestamp, for a re-add too.
func TestPlacementStore_ReRegisterRefreshesCreatedAt(t *testing.T) {
	s := newPlacementStore("")
	req := PlacementRequest{Hash: testHash, SeriesDir: "Show", Kind: PlacementEpisode, Season: 1}
	if _, err := s.register(req); err != nil {
		t.Fatal(err)
	}
	p, _ := s.get(testHash)
	p.CreatedAt = p.CreatedAt.Add(-48 * time.Hour)
	if err := s.update(p); err != nil {
		t.Fatal(err)
	}
	again, err := s.register(req)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(again.CreatedAt) > time.Minute {
		t.Fatalf("re-registering must refresh CreatedAt, got %v", again.CreatedAt)
	}
}

// A finished torrent's file deleted outside the app is not recreated (rain would download it
// again): Open fails with ErrPlacedFileMissing. Before completion a missing file is normal.
func TestPlacedStorage_OpenRefusesToRecreateADeletedFinishedFile(t *testing.T) {
	_, ps, lib := newPlaced(t, false, "ep.mkv")
	file := filepath.Join(lib, "Show", "Season 01", "ep.mkv")

	f, _, err := ps.Open("ep.mkv", 4)
	if err != nil {
		t.Fatalf("an unfinished torrent creates its files: %v", err)
	}
	_ = f.Close()

	p, _ := ps.store.get(testHash)
	p.Completed = true
	if err := ps.store.update(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ps.Open("ep.mkv", 4); !errors.Is(err, ErrPlacedFileMissing) {
		t.Fatalf("a deleted finished file must not be recreated, got %v", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("nothing may be created")
	}
}
