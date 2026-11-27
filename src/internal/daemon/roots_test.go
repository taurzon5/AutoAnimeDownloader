package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/nyaa"
	"AutoAnimeDownloader/src/internal/torrents"
)

func rootNames(roots []files.DownloadRoot) []string {
	out := make([]string, len(roots))
	for i, r := range roots {
		out[i] = r.Name
	}
	return out
}

// The order a torrent's root is tried in (decisions/154): the anime's override, the root already
// holding its series, then download_roots in fill order, each once. Hardlink mode, or no roots
// configured, has the library alone.
func TestRootOrder(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
	a, b, c := filepath.Join(lib, "a"), filepath.Join(lib, "b"), filepath.Join(lib, "c")
	configs.DownloadRoots = []files.DownloadRoot{{Name: "ssd", Path: a}, {Name: "hdd", Path: b}, {Name: "nas", Path: c}}
	for _, dir := range []string{a, b, c} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	series := &anilist.Series{Key: 9}
	target := torrentTarget{anime: titledAnime(1, "Show", anilist.MediaFormatTV), series: series, episode: 1}

	if got := rootNames(rootOrder(configs, backend, target, "")); !equalStrings(got, []string{"ssd", "hdd", "nas"}) {
		t.Errorf("no override, nothing held: %v, want the fill order", got)
	}
	if got := rootNames(rootOrder(configs, backend, target, "nas")); !equalStrings(got, []string{"nas", "ssd", "hdd"}) {
		t.Errorf("override nas: %v", got)
	}
	if got := rootNames(rootOrder(configs, backend, target, "gone")); !equalStrings(got, []string{"ssd", "hdd", "nas"}) {
		t.Errorf("an override naming no root: %v, want the fill order", got)
	}
	// A season of the series already lives on hdd: the next one goes next to it.
	if err := backend.SetPlacement(torrents.PlacementRequest{Hash: "1111111111111111111111111111111111111111", SeriesDir: "Show", SeriesID: seriesID(target), Kind: torrents.PlacementEpisode, Season: 1, RootPath: b, RootName: "hdd"}); err != nil {
		t.Fatal(err)
	}
	if got := rootNames(rootOrder(configs, backend, target, "")); !equalStrings(got, []string{"hdd", "ssd", "nas"}) {
		t.Errorf("series on hdd: %v", got)
	}
	if got := rootNames(rootOrder(configs, backend, target, "nas")); !equalStrings(got, []string{"nas", "hdd", "ssd"}) {
		t.Errorf("override beats the series root: %v", got)
	}

	// A root whose folder isn't there (an unmounted drive) is never picked, even as the override.
	if err := os.Remove(c); err != nil {
		t.Fatal(err)
	}
	if got := rootNames(rootOrder(configs, backend, target, "nas")); !equalStrings(got, []string{"hdd", "ssd"}) {
		t.Errorf("nas unmounted: %v, want it left out", got)
	}

	configs.LibraryMode = files.LibraryModeHardlink
	if got := rootOrder(configs, backend, target, "nas"); len(got) != 1 || got[0].Path != lib || got[0].Name != files.LibraryRootName {
		t.Errorf("hardlink mode: %+v, want the library alone", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A root that can't take a torrent on top of what is reserved there passes it to the next root;
// each root counts its own reservations, and a pack's episodes go where the pack went.
func TestDiskBudgetFillsRootsInOrder(t *testing.T) {
	configs, size := budgetConfig(t)
	configs.LibraryMode = files.LibraryModeDirect
	a, b := filepath.Join(configs.CompletedAnimePath, "a"), filepath.Join(configs.CompletedAnimePath, "b")
	configs.DownloadRoots = []files.DownloadRoot{{Name: "first", Path: a}, {Name: "second", Path: b}}
	// The guard never blocks on a folder it can't measure: the roots must exist.
	for _, dir := range []string{a, b} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Two drives: each root counts only its own reservations.
	defer func(orig func(a, b string) bool) { sameFilesystem = orig }(sameFilesystem)
	sameFilesystem = func(x, y string) bool { return x == y }
	disk := newDiskBudget(nil, nil).withRoots(configs, nil, nil)
	order := configs.Roots()

	root, _, err := disk.reserve(configs, nyaa.TorrentResult{MagnetLink: fakeMagnet(1), Size: size}, order)
	if err != nil || root.Name != "first" {
		t.Fatalf("first torrent: %+v, %v; want the first root", root, err)
	}
	root, _, err = disk.reserve(configs, nyaa.TorrentResult{MagnetLink: fakeMagnet(2), Size: size}, order)
	if err != nil || root.Name != "second" {
		t.Fatalf("second torrent: %+v, %v; want the second root, the first one is reserved", root, err)
	}
	if _, _, err = disk.reserve(configs, nyaa.TorrentResult{MagnetLink: fakeMagnet(3), Size: size}, order); err == nil {
		t.Fatal("a third torrent fits nowhere and must be refused")
	}
	// The same pack again (another of its episodes) goes where it went, for free.
	root, _, err = disk.reserve(configs, nyaa.TorrentResult{MagnetLink: fakeMagnet(2), Size: size}, order)
	if err != nil || root.Name != "second" {
		t.Fatalf("the pack again: %+v, %v; want the second root, no new bytes", root, err)
	}
}

// The placement records the chosen root, unless it is the library, which stays implicit so a moved
// library keeps its seeds (decisions/136); its files resolve under that root.
func TestRegisterPlacementRecordsItsRoot(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
	other := t.TempDir()
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	target := torrentTarget{anime: titledAnime(3, "Show", anilist.MediaFormatTV), episode: 1}

	if err := registerPlacement(configs, backend, directMagnet, target, files.DownloadRoot{Name: "hdd", Path: other}); err != nil {
		t.Fatal(err)
	}
	p, err := backend.PlaceFiles(directHash, []string{"ep01.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if p.RootPath != other || p.RootName != "hdd" || filepath.Dir(filepath.Dir(p.Files[0].Path)) != filepath.Join(other, "Show") {
		t.Fatalf("placement = %+v; want it under %s", p, other)
	}
	// Read back through the backend, which resolves against its library: the placement's own root wins.
	if got, _ := backend.Placement(directHash); filepath.Dir(filepath.Dir(got.Files[0].Path)) != filepath.Join(other, "Show") || got.SeriesDir != filepath.Join(other, "Show") {
		t.Fatalf("resolved placement = %+v; want it under %s, not the library", got, other)
	}

	magnet := "magnet:?xt=urn:btih:2222222222222222222222222222222222222222"
	if err := registerPlacement(configs, backend, magnet, target, files.DownloadRoot{Name: files.LibraryRootName, Path: lib}); err != nil {
		t.Fatal(err)
	}
	if p, _ := backend.Placement("2222222222222222222222222222222222222222"); p.RootPath != "" || p.RootName != files.LibraryRootName {
		t.Fatalf("library placement = %+v; want no root path", p)
	}
}

// Two roots on one drive share its free space: what one reserved counts against the other.
func TestDiskBudgetRootsOnOneDriveShareTheSpace(t *testing.T) {
	configs, size := budgetConfig(t)
	configs.LibraryMode = files.LibraryModeDirect
	a, b := filepath.Join(configs.CompletedAnimePath, "a"), filepath.Join(configs.CompletedAnimePath, "b")
	configs.DownloadRoots = []files.DownloadRoot{{Name: "first", Path: a}, {Name: "second", Path: b}}
	for _, dir := range []string{a, b} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	disk := newDiskBudget(nil, nil).withRoots(configs, nil, nil)
	if _, _, err := disk.reserve(configs, nyaa.TorrentResult{MagnetLink: fakeMagnet(1), Size: size}, configs.Roots()); err != nil {
		t.Fatal(err)
	}
	if root, _, err := disk.reserve(configs, nyaa.TorrentResult{MagnetLink: fakeMagnet(2), Size: size}, configs.Roots()); err == nil {
		t.Fatalf("the second torrent went to %+v; both roots are on one drive, already 60%% reserved", root)
	}
}

// A download root the user moved: its old folder is gone and download_roots names it elsewhere, so
// its placements follow; one that is merely gone (unmounted) is left alone (decisions/154).
func TestFollowMovedRoots(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
	moved := t.TempDir()
	configs.DownloadRoots = []files.DownloadRoot{{Name: "hdd", Path: moved}, {Name: "nas", Path: filepath.Join(lib, "nas-unmounted")}}
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	old := filepath.Join(lib, "old-hdd")
	for hash, req := range map[string]torrents.PlacementRequest{
		"1111111111111111111111111111111111111111": {SeriesDir: "A", Kind: torrents.PlacementPack, RootPath: old, RootName: "hdd"},
		"2222222222222222222222222222222222222222": {SeriesDir: "B", Kind: torrents.PlacementPack, RootPath: filepath.Join(lib, "gone"), RootName: "nas"},
	} {
		req.Hash = hash
		if err := backend.SetPlacement(req); err != nil {
			t.Fatal(err)
		}
	}
	// The old folder is still there: the user pointed the name elsewhere without moving anything.
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	followMovedRoots(configs, backend)
	if p, _ := backend.Placement("1111111111111111111111111111111111111111"); p.RootPath != old {
		t.Fatalf("old folder still there: placement re-pointed to %q", p.RootPath)
	}
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	followMovedRoots(configs, backend)
	if p, _ := backend.Placement("1111111111111111111111111111111111111111"); p.RootPath != moved {
		t.Errorf("moved root: placement at %q, want %q", p.RootPath, moved)
	}
	if p, _ := backend.Placement("2222222222222222222222222222222222222222"); p.RootPath != filepath.Join(lib, "gone") {
		t.Errorf("a root whose new folder isn't there either must be left alone, got %q", p.RootPath)
	}
}

// A renamed download folder is followed only when the torrent's files are at the new path: with
// nothing there, re-pointing it would download everything again into the wrong folder.
func TestFollowMovedRoots_OnlyWithTheFilesThere(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
	newer := t.TempDir()
	configs.DownloadRoots = []files.DownloadRoot{{Name: "ssd", Path: newer}}
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	old := filepath.Join(lib, "unplugged")
	const hash = "3333333333333333333333333333333333333333"
	if err := backend.SetPlacement(torrents.PlacementRequest{Hash: hash, SeriesDir: "Show", Kind: torrents.PlacementPack, RootPath: old, RootName: "ssd"}); err != nil {
		t.Fatal(err)
	}
	placed, err := backend.PlaceFiles(hash, []string{"Show - 01 [1080p].mkv"})
	if err != nil || len(placed.Files) != 1 {
		t.Fatalf("place files: %+v, %v", placed.Files, err)
	}
	rel, err := filepath.Rel(old, placed.Files[0].Path)
	if err != nil {
		t.Fatal(err)
	}

	followMovedRoots(configs, backend)
	if p, _ := backend.Placement(hash); p.RootPath != old {
		t.Fatalf("files not in the new folder: placement re-pointed to %q", p.RootPath)
	}

	if err := os.MkdirAll(filepath.Dir(filepath.Join(newer, rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newer, rel), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	followMovedRoots(configs, backend)
	if p, _ := backend.Placement(hash); p.RootPath != newer {
		t.Errorf("files moved with the folder: placement at %q, want %q", p.RootPath, newer)
	}
}

// A torrent added again keeps the root of the placement it already has (register keeps the first,
// decisions/136): the budget counts it there, whatever the order says.
func TestPlacedOrderKeepsTheTorrentsRoot(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	order := []files.DownloadRoot{{Name: "ssd", Path: "/mnt/ssd"}}
	if got := placedOrder(configs, backend, directMagnet, order); len(got) != 1 || got[0].Name != "ssd" {
		t.Fatalf("no placement: %+v, want the order", got)
	}
	hdd := t.TempDir()
	if err := backend.SetPlacement(torrents.PlacementRequest{Hash: directHash, SeriesDir: "Show", Kind: torrents.PlacementPack, RootPath: hdd, RootName: "hdd"}); err != nil {
		t.Fatal(err)
	}
	if got := placedOrder(configs, backend, directMagnet, order); len(got) != 1 || got[0].Name != "hdd" || got[0].Path != hdd {
		t.Fatalf("placed on hdd: %+v, want hdd only", got)
	}
	// Its root gone (an unmounted drive): nowhere, never the folder recreated.
	if err := os.Remove(hdd); err != nil {
		t.Fatal(err)
	}
	if got := placedOrder(configs, backend, directMagnet, order); got == nil || len(got) != 0 {
		t.Fatalf("root gone: %+v, want an empty order", got)
	}
	if _, _, err := (*diskBudget)(nil).reserve(configs, nyaa.TorrentResult{MagnetLink: directMagnet}, []files.DownloadRoot{}); err == nil {
		t.Fatal("an empty order must refuse the torrent")
	}
}

// Reordering download_roots only changes where new torrents go (decisions/154): a torrent already
// placed keeps its root and its files, a retry of it is counted there, and a new episode of a
// series still goes next to the series, even when that root is no longer first.
func TestReorderingRootsMovesNothing(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
	ssd, hdd := filepath.Join(lib, "ssd"), filepath.Join(lib, "hdd")
	for _, dir := range []string{ssd, hdd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	configs.DownloadRoots = []files.DownloadRoot{{Name: "ssd", Path: ssd}, {Name: "hdd", Path: hdd}}
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	show := torrentTarget{anime: titledAnime(1, "Show", anilist.MediaFormatTV), series: &anilist.Series{Key: 9}, episode: 2}
	other := "1111111111111111111111111111111111111111"
	placed := map[string]torrents.PlacementRequest{
		directHash: {SeriesDir: "Show", SeriesID: seriesID(show), Kind: torrents.PlacementEpisode, Season: 1, RootPath: ssd, RootName: "ssd"},
		other:      {SeriesDir: "Other", Kind: torrents.PlacementPack, RootPath: hdd, RootName: "hdd"},
	}
	onDisk := map[string]string{}
	for hash, req := range placed {
		req.Hash = hash
		if err := backend.SetPlacement(req); err != nil {
			t.Fatal(err)
		}
		p, err := backend.PlaceFiles(hash, []string{req.SeriesDir + " - 01.mkv"})
		if err != nil {
			t.Fatal(err)
		}
		path := p.Resolve(lib).Files[0].Path
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		onDisk[hash] = path
	}

	// hdd moved up: the highest priority now.
	configs.DownloadRoots = []files.DownloadRoot{configs.DownloadRoots[1], configs.DownloadRoots[0]}
	followMovedRoots(configs, backend)

	for hash, req := range placed {
		p, _ := backend.Placement(hash)
		if p.RootPath != req.RootPath || p.RootName != req.RootName {
			t.Errorf("%s: placement moved to %s (%q), want %s (%q)", req.SeriesDir, p.RootPath, p.RootName, req.RootPath, req.RootName)
		}
		if got := p.Resolve(lib).Files[0].Path; got != onDisk[hash] {
			t.Errorf("%s: file resolves to %s, want %s", req.SeriesDir, got, onDisk[hash])
		}
		if _, err := os.Stat(onDisk[hash]); err != nil {
			t.Errorf("%s: file gone after the reorder: %v", req.SeriesDir, err)
		}
	}
	if got := placedOrder(configs, backend, directMagnet, rootOrder(configs, backend, show, "")); len(got) != 1 || got[0].Path != ssd {
		t.Errorf("a retry of a placed torrent: %+v, want its own root ssd", got)
	}
	if got := rootNames(rootOrder(configs, backend, show, "")); !equalStrings(got, []string{"ssd", "hdd"}) {
		t.Errorf("a new episode of a series on ssd: %v, want ssd first, next to the series", got)
	}
	fresh := torrentTarget{anime: titledAnime(2, "Fresh", anilist.MediaFormatTV), series: &anilist.Series{Key: 20}, episode: 1}
	if got := rootNames(rootOrder(configs, backend, fresh, "")); !equalStrings(got, []string{"hdd", "ssd"}) {
		t.Errorf("a new anime: %v, want the new order", got)
	}
}

// DiskLow counts what a torrent still writes on its own root: a full root next to one with space
// is not low (decisions/154, decisions/093).
func TestDiskLowCountsPerRoot(t *testing.T) {
	configs, _ := budgetConfig(t)
	configs.LibraryMode = files.LibraryModeDirect
	configs.MinFreeDiskGB = 0
	a, b := filepath.Join(configs.CompletedAnimePath, "a"), filepath.Join(configs.CompletedAnimePath, "b")
	for _, dir := range []string{a, b} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	configs.DownloadRoots = []files.DownloadRoot{{Name: "a", Path: a}, {Name: "b", Path: b}}
	defer func(orig func(x, y string) bool) { sameFilesystem = orig }(sameFilesystem)
	sameFilesystem = func(x, y string) bool { return x == y }
	_, free, err := files.DiskSpace(a)
	if err != nil {
		t.Skip(err)
	}
	const hash = "3333333333333333333333333333333333333333"
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = configs.CompletedAnimePath
	if err := backend.SetPlacement(torrents.PlacementRequest{Hash: hash, SeriesDir: "Show", Kind: torrents.PlacementPack, RootPath: a, RootName: "a"}); err != nil {
		t.Fatal(err)
	}
	session := []torrents.TorrentInfo{{Hash: hash, BytesTotal: int64(free) + 1<<30}}
	if DiskLow(configs, backend, session, nil) {
		t.Error("root b still takes torrents: not low")
	}
	configs.DownloadRoots = configs.DownloadRoots[:1]
	if !DiskLow(configs, backend, session, nil) {
		t.Error("the only root can't take a torrent: low")
	}
}

// Each folder fills until its drive would keep less than its own min_free_gb, then the next one in
// the list takes the torrents; unset, it keeps min_free_disk_gb (decisions/154).
func TestRootsFillUpToTheirOwnLimit(t *testing.T) {
	configs, size := budgetConfig(t)
	configs.LibraryMode = files.LibraryModeDirect
	configs.MinFreeDiskGB = 0
	a, b := filepath.Join(configs.CompletedAnimePath, "a"), filepath.Join(configs.CompletedAnimePath, "b")
	for _, dir := range []string{a, b} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	defer func(orig func(x, y string) bool) { sameFilesystem = orig }(sameFilesystem)
	sameFilesystem = func(x, y string) bool { return x == y }
	_, free, err := files.DiskSpace(a)
	if err != nil {
		t.Skip(err)
	}
	// First drive: keep more than it has left once this torrent is in.
	full := int(free>>30) + 1
	configs.DownloadRoots = []files.DownloadRoot{{Name: "first", Path: a, MinFreeGB: &full}, {Name: "second", Path: b}}
	root, _, err := newDiskBudget(nil, nil).withRoots(configs, nil, nil).reserve(configs, nyaa.TorrentResult{MagnetLink: fakeMagnet(1), Size: size}, configs.Roots())
	if err != nil || root.Name != "second" {
		t.Fatalf("first past its limit: %+v, %v; want the second", root, err)
	}
	zero := 0
	configs.DownloadRoots[0].MinFreeGB = &zero
	root, _, err = newDiskBudget(nil, nil).withRoots(configs, nil, nil).reserve(configs, nyaa.TorrentResult{MagnetLink: fakeMagnet(2), Size: size}, configs.Roots())
	if err != nil || root.Name != "first" {
		t.Fatalf("first under its limit: %+v, %v; want the first", root, err)
	}
	// Unset, the global min_free_disk_gb applies to it.
	configs.DownloadRoots[0].MinFreeGB = nil
	configs.MinFreeDiskGB = full
	if got := configs.RootMinFreeGB(a); got != full {
		t.Errorf("unset limit = %d, want min_free_disk_gb %d", got, full)
	}
}

// The incomplete folder on root b's drive: a torrent staging for root a (another drive) first
// downloads onto b's drive, so b can't also take one that only fits alone (decisions/154,
// decisions/141).
func TestDiskBudgetCountsOtherRootsStagingOnTheIncompleteDrive(t *testing.T) {
	configs, size := budgetConfig(t)
	configs.LibraryMode = files.LibraryModeDirect
	base := configs.CompletedAnimePath
	a, b, staging := filepath.Join(base, "a"), filepath.Join(base, "b"), filepath.Join(base, "staging")
	for _, dir := range []string{a, b, staging} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	configs.DownloadRoots = []files.DownloadRoot{{Name: "a", Path: a}, {Name: "b", Path: b}}
	configs.IncompletePath = staging
	configs.IncompleteMinFreeDiskGB = 0
	defer func(orig func(x, y string) bool) { sameFilesystem = orig }(sameFilesystem)
	volume := func(p string) string {
		if p == a {
			return "drive-a"
		}
		return "drive-b" // b, the staging folder and the library
	}
	sameFilesystem = func(x, y string) bool { return volume(x) == volume(y) }

	disk := newDiskBudget(nil, nil).withRoots(configs, nil, nil)
	root, _, err := disk.reserve(configs, nyaa.TorrentResult{MagnetLink: fakeMagnet(1), Size: size}, configs.Roots())
	if err != nil || root.Name != "a" {
		t.Fatalf("first torrent: %+v, %v; want a", root, err)
	}
	if root, _, err := disk.reserve(configs, nyaa.TorrentResult{MagnetLink: fakeMagnet(2), Size: size}, []files.DownloadRoot{{Name: "b", Path: b}}); err == nil {
		t.Fatalf("b took %+v; a's staging download lands on b's drive first, so it must not fit", root)
	}
}
