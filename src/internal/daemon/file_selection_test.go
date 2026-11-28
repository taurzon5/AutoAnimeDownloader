package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/nyaa"
	"AutoAnimeDownloader/src/internal/torrents"
)

// cacheFileList puts a Nyaa file list in the cache, as the picker's detail read leaves it, and
// turns the cache off again after the test.
func cacheFileList(t *testing.T, nyaaID int, list []nyaa.TorrentFile) {
	t.Helper()
	dir := t.TempDir()
	nyaa.SetFileListCacheDir(dir)
	t.Cleanup(func() { nyaa.SetFileListCacheDir("") })
	data, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", nyaaID)), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func qbitConfig(lib string) *files.Config {
	c := directConfig(lib)
	c.TorrentClient = files.TorrentClientQBittorrent
	return c
}

func frierenPackFiles() []nyaa.TorrentFile {
	var list []nyaa.TorrentFile
	for ep := 1; ep <= 12; ep++ {
		list = append(list, nyaa.TorrentFile{Name: fmt.Sprintf("[SubsPlease] Frieren S2/Frieren S2 - %02d (1080p).mkv", ep), SizeBytes: 100})
	}
	return append(list, nyaa.TorrentFile{Name: "[SubsPlease] Frieren S2/NCOP.mkv", SizeBytes: 50})
}

// A whole-title pick with files unticked (decisions/165): refused with the built-in client before
// anything is added; with qBittorrent the placement carries the skip, the disk guard counts only
// the ticked bytes, and the unticked episodes get no record.
func TestDownloadStandaloneAnimeWithMagnet_FileChoice(t *testing.T) {
	defer mockAniListRouter(t, emptyAniListPage, mediaForFrierenSeason2)()
	cacheFileList(t, 4242, frierenPackFiles())
	name := "[SubsPlease] Frieren Season 2 (01-12) [1080p]"
	skip := []string{"[SubsPlease] Frieren S2/Frieren S2 - 11 (1080p).mkv", "[SubsPlease] Frieren S2/Frieren S2 - 12 (1080p).mkv", "[SubsPlease] Frieren S2/NCOP.mkv"}
	picked := PickedRelease{Name: name, NyaaID: 4242, SizeBytes: 1250, Skip: skip}

	builtin := torrents.NewFakeBackend()
	if _, err := DownloadStandaloneAnimeWithMagnet(&mockFileManagerForEpisodes{}, builtin, directConfig(t.TempDir()), 700, standaloneMagnet, picked); !errors.Is(err, ErrFileSelectionNeedsQBittorrent) {
		t.Fatalf("built-in client: err = %v, want ErrFileSelectionNeedsQBittorrent", err)
	}
	if len(builtin.List()) != 0 {
		t.Fatal("a refused file choice must add nothing")
	}

	if got := picked.selectedBytes(); got != 1000 {
		t.Fatalf("selectedBytes = %d, want 1000 (ten episodes of 100)", got)
	}

	fm := &mockFileManagerForEpisodes{}
	backend := torrents.NewFakeBackend()
	lib := t.TempDir()
	backend.LibraryRoot = lib
	n, err := DownloadStandaloneAnimeWithMagnet(fm, backend, qbitConfig(lib), 700, standaloneMagnet, picked)
	if err != nil {
		t.Fatalf("DownloadStandaloneAnimeWithMagnet: %v", err)
	}
	if n != 10 {
		t.Fatalf("saved %d episodes, want 10 (11 and 12 unticked)", n)
	}
	for _, ep := range fm.savedEpisodes {
		if ep.EpisodeNumber > 10 {
			t.Errorf("episode %d was recorded though its file is unticked", ep.EpisodeNumber)
		}
	}
	hash, _ := torrents.InfoHash(standaloneMagnet)
	p, ok := backend.Placement(hash)
	if !ok || !slices.Equal(p.Skip, skip) {
		t.Fatalf("placement skip = %v (placed %v), want %v", p.Skip, ok, skip)
	}
}

// packOfThree places a pack of anime 3's episodes 1-3 for qBittorrent, its files on disk, one
// record per episode carrying the library paths.
func packOfThree(t *testing.T) (*files.Config, *torrents.FakeBackend, *mockFileManagerForEpisodes, []torrents.PlacedFile) {
	t.Helper()
	lib := t.TempDir()
	configs := qbitConfig(lib)
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	records, paths := packInLibrary(t, configs, backend, true)
	head := records[0]
	var saved []files.EpisodeStruct
	for ep := 1; ep <= 3; ep++ {
		r := head
		r.EpisodeNumber = ep
		r.AnimeName = "Show"
		r.LibraryPaths = paths
		saved = append(saved, r)
	}
	p, _ := backend.Placement(directHash)
	return configs, backend, &mockFileManagerForEpisodes{savedEpisodes: saved}, p.Files
}

type shokoFolders bool

func (s shokoFolders) InShoko(*files.Config, string) bool { return bool(s) }
func (shokoFolders) RequestImport(*files.Config)          {}

// Unticking a written file deletes it and drops the record of the episode it leaves with no file,
// blocked when asked; ticking it again downloads it and brings the record back (decisions/165).
func TestChooseTorrentFiles_SkipDeletesAndWantBringsBack(t *testing.T) {
	defer mockAniListRouter(t, emptyAniListPage, `{"data": {"Media": null}}`)()
	configs, backend, fm, placed := packOfThree(t)
	second := placed[1]

	result, err := ChooseTorrentFiles(fm, backend, configs, nil, directHash, []string{second.Torrent}, nil, true)
	if err != nil {
		t.Fatalf("skip: %v", err)
	}
	if result.Skipped != 1 || result.Deleted != 1 || result.RecordsRemoved != 1 {
		t.Fatalf("result = %+v, want 1 skipped, 1 deleted, 1 record removed", result)
	}
	if exists(second.Path) {
		t.Fatal("the unticked file must be deleted")
	}
	if !exists(placed[0].Path) || !exists(placed[2].Path) {
		t.Fatal("the other files stay")
	}
	ep2 := files.EpisodeKey{AnimeID: 3, Episode: 2}
	if !slices.Contains(fm.deletedEpisodeKeys, ep2) || !slices.Contains(fm.blockedEpisodeKeys, ep2) {
		t.Fatalf("deleted %v, blocked %v; want episode 2 in both", fm.deletedEpisodeKeys, fm.blockedEpisodeKeys)
	}
	for _, r := range fm.savedEpisodes {
		if r.EpisodeNumber != 2 && slices.Contains(r.LibraryPaths, second.Path) {
			t.Errorf("episode %d still lists the deleted file in %v", r.EpisodeNumber, r.LibraryPaths)
		}
	}
	if p, _ := backend.Placement(directHash); !p.Files[1].Skipped {
		t.Fatal("the placement must mark the file skipped")
	}

	// The record of episode 2 is gone from the saved list now, as the file manager would have it.
	var left []files.EpisodeStruct
	for _, r := range fm.savedEpisodes {
		if r.EpisodeNumber != 2 {
			left = append(left, r)
		}
	}
	fm.savedEpisodes = left
	result, err = ChooseTorrentFiles(fm, backend, configs, nil, directHash, nil, []string{second.Torrent}, false)
	if err != nil {
		t.Fatalf("want: %v", err)
	}
	if result.Wanted != 1 || result.RecordsAdded != 1 {
		t.Fatalf("result = %+v, want 1 wanted, 1 record added", result)
	}
	if !slices.Contains(backend.WantedCalls, directHash) {
		t.Fatal("the backend must be asked to download the file again")
	}
	back := false
	for _, r := range fm.savedEpisodes {
		back = back || (r.EpisodeNumber == 2 && r.EpisodeHash == directHash && r.AnimeID == 3)
	}
	if !back {
		t.Fatalf("episode 2's record must come back: %+v", fm.savedEpisodes)
	}
	if p, _ := backend.Placement(directHash); p.Files[1].Skipped || p.Completed {
		t.Fatalf("the placement must clear the mark and reopen: %+v", p)
	}
}

// A file in one of Shoko's import folders is Shoko's (decisions/152): unticking it skips it but
// never deletes it. The built-in client, an unknown path or a path in both lists change nothing.
func TestChooseTorrentFiles_GuardsAndRefusals(t *testing.T) {
	defer mockAniListRouter(t, emptyAniListPage, `{"data": {"Media": null}}`)()
	configs, backend, fm, placed := packOfThree(t)

	result, err := ChooseTorrentFiles(fm, backend, configs, shokoFolders(true), directHash, []string{placed[0].Torrent}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 0 || result.Kept != 1 || !exists(placed[0].Path) {
		t.Fatalf("result = %+v, file there %v; want it kept in Shoko's folder", result, exists(placed[0].Path))
	}
	// Its episode still has its file: the record stays (decisions/152), and a later change to
	// the same torrent doesn't forget it.
	if result.RecordsRemoved != 0 || len(fm.deletedEpisodeKeys) != 0 {
		t.Fatalf("result = %+v, deleted %v; a kept file keeps its record", result, fm.deletedEpisodeKeys)
	}
	if _, err := ChooseTorrentFiles(fm, backend, configs, shokoFolders(true), directHash, []string{placed[2].Torrent}, nil, false); err != nil {
		t.Fatal(err)
	}
	if len(fm.deletedEpisodeKeys) != 0 {
		t.Fatalf("deleted %v; files kept for Shoko keep their records on a later change too", fm.deletedEpisodeKeys)
	}

	// A partial file is no file of Shoko's: it never imports a ".part".
	partCfg, partBackend, partFM, partPlaced := packOfThree(t)
	p, _ := partBackend.Placement(directHash)
	part := p.DiskPath(partPlaced[1]) + ".part"
	if err := os.WriteFile(part, []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := partBackend.RedownloadPlacement(directHash, ""); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(partPlaced[1].Path)
	result, err = ChooseTorrentFiles(partFM, partBackend, partCfg, shokoFolders(true), directHash, []string{partPlaced[1].Torrent}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 1 || result.Kept != 0 || exists(part) {
		t.Fatalf("result = %+v, part there %v; want the partial file deleted even in Shoko's folder", result, exists(part))
	}

	builtin := directConfig(configs.CompletedAnimePath)
	if _, err := ChooseTorrentFiles(fm, backend, builtin, nil, directHash, []string{placed[2].Torrent}, nil, false); !errors.Is(err, ErrFileSelectionNeedsQBittorrent) {
		t.Fatalf("built-in client: err = %v", err)
	}
	if _, err := ChooseTorrentFiles(fm, backend, configs, nil, directHash, []string{"Pack/nope.mkv"}, nil, false); !errors.Is(err, ErrUnknownTorrentFile) {
		t.Fatalf("unknown path: err = %v", err)
	}
	if _, err := ChooseTorrentFiles(fm, backend, configs, nil, directHash, []string{placed[2].Torrent}, []string{placed[2].Torrent}, false); !errors.Is(err, ErrUnknownTorrentFile) {
		t.Fatalf("path in both lists: err = %v", err)
	}
	if _, err := ChooseTorrentFiles(fm, backend, configs, nil, "ffffffffffffffffffffffffffffffffffffffff", []string{"x"}, nil, false); !errors.Is(err, torrents.ErrFilesNotPlanned) {
		t.Fatalf("no placement: err = %v", err)
	}
	if !exists(placed[2].Path) {
		t.Fatal("a refused choice deletes nothing")
	}
}

// packSkips reads a placement's skipped files as episodes of the anime: episode 2's only file is
// unticked, 1 and 3 are held (decisions/165).
func TestPackSkips_ReadsTheUntickedEpisodes(t *testing.T) {
	_, backend, _, placed := packOfThree(t)
	if err := backend.SkipPlacedFiles(directHash, []string{placed[1].Torrent}); err != nil {
		t.Fatal(err)
	}
	skipped := packSkips(backend)
	for ep, want := range map[int]bool{1: false, 2: true, 3: false} {
		if got := skipped(directHash, 0, 3, nil, ep); got != want {
			t.Errorf("episode %d skipped = %v, want %v", ep, got, want)
		}
	}
	if skipped("ffffffffffffffffffffffffffffffffffffffff", 0, 3, nil, 2) {
		t.Error("a torrent with no placement skips nothing")
	}
}

// A torrent the app already has, with the episode's file unticked, is no candidate for that
// episode: adding it again would record the episode with no file. A pick of a torrent the app
// already has keeps that torrent's own choice (decisions/165).
func TestRepicksUntickedAndPlacedTorrentKeepsItsChoice(t *testing.T) {
	_, backend, _, placed := packOfThree(t)
	if err := backend.SkipPlacedFiles(directHash, []string{placed[1].Torrent}); err != nil {
		t.Fatal(err)
	}
	anime := titledAnime(3, "Show", "TV")
	episode := func(n int) torrentTarget { return torrentTarget{anime: anime, episode: n} }
	if !repicksUnticked(backend, directMagnet, episode(2)) {
		t.Error("episode 2's file is unticked in this torrent: it must not be picked again for it")
	}
	if repicksUnticked(backend, directMagnet, episode(1)) {
		t.Error("episode 1's file is there: the torrent is a fine candidate for it")
	}
	if !repicksUnticked(backend, directMagnet, torrentTarget{anime: anime, batch: true}) {
		t.Error("a pack target takes the whole torrent: an unticked episode of the anime refuses it")
	}
	if repicksUnticked(backend, "magnet:?xt=urn:btih:ffffffffffffffffffffffffffffffffffffffff", episode(2)) {
		t.Error("a torrent the app doesn't have is always a candidate")
	}

	// A pick of that torrent records none of its unticked episodes, and the pass doesn't try it for
	// them.
	if gone := untickedEpisodes(backend, directHash, episode(0), []int{1, 2, 3}); len(gone) != 1 || !gone[2] {
		t.Errorf("unticked episodes = %v, want only 2", gone)
	}
	candidates := []nyaa.TorrentResult{{Name: "pack", MagnetLink: directMagnet}, {Name: "other", MagnetLink: "magnet:?xt=urn:btih:ffffffffffffffffffffffffffffffffffffffff"}}
	if left := withoutUntickedRepicks(backend, candidates, episode(2)); len(left) != 1 || left[0].Name != "other" {
		t.Errorf("candidates left = %+v, want only the other torrent", left)
	}

	picked := withoutSkipForPlacedTorrent(backend, directMagnet, PickedRelease{Skip: []string{placed[0].Torrent}})
	if picked.Skip != nil {
		t.Fatalf("skip = %v, want none for a torrent the app already has", picked.Skip)
	}
	fresh := withoutSkipForPlacedTorrent(backend, "magnet:?xt=urn:btih:ffffffffffffffffffffffffffffffffffffffff", PickedRelease{Skip: []string{"x"}})
	if len(fresh.Skip) != 1 {
		t.Fatal("a new torrent keeps the pick's choice")
	}
}

// The episode replace asks first, before it deletes the episode's download: a magnet of a torrent
// the app has with that episode's file unticked is refused (decisions/165).
func TestCheckEpisodeMagnet(t *testing.T) {
	defer mockAniListRouter(t, emptyAniListPage, `{"data": {"Media": {"id": 3, "format": "TV", "status": "FINISHED", "episodes": 3,
		"title": {"english": "Show", "romaji": "Show"}, "synonyms": [], "relations": {"edges": []},
		"coverImage": {"large": "", "medium": ""}, "airingSchedule": {"nodes": []}}}}`)()
	configs, backend, fm, placed := packOfThree(t)
	if err := backend.SkipPlacedFiles(directHash, []string{placed[1].Torrent}); err != nil {
		t.Fatal(err)
	}
	if err := CheckEpisodeMagnet(fm, backend, configs, 3, 2, directMagnet); !errors.Is(err, ErrEpisodeFileUnticked) {
		t.Fatalf("episode 2: err = %v, want ErrEpisodeFileUnticked", err)
	}
	if err := CheckEpisodeMagnet(fm, backend, configs, 3, 1, directMagnet); err != nil {
		t.Fatalf("episode 1 has its file: err = %v", err)
	}
	if err := CheckEpisodeMagnet(fm, backend, configs, 3, 2, "magnet:?xt=urn:btih:ffffffffffffffffffffffffffffffffffffffff"); err != nil {
		t.Fatalf("a torrent the app doesn't have: err = %v", err)
	}
}

// In the pass, a torrent the app has with the episode's file unticked is no candidate: another
// release is downloaded as a single episode, and with none the episode is neither an issue nor a
// hold on the later ones (decisions/165).
func TestProcessAnimeEpisodes_LeavesOutTheUntickedTorrent(t *testing.T) {
	configs, backend, _, placed := packOfThree(t)
	if err := backend.SkipPlacedFiles(directHash, []string{placed[1].Torrent}); err != nil {
		t.Fatal(err)
	}
	anime := finishedAnime(3, "Show", 3, 0)
	saved := []files.EpisodeStruct{
		{AnimeID: 3, EpisodeNumber: 1, EpisodeHash: directHash, IsBatch: true, BatchStart: 1, BatchEnd: 3},
		{AnimeID: 3, EpisodeNumber: 3, EpisodeHash: directHash, IsBatch: true, BatchStart: 1, BatchEnd: 3},
	}
	other := nyaa.TorrentResult{Name: "[Group] Show - 02 [1080p].mkv", MagnetLink: "magnet:?xt=urn:btih:ffffffffffffffffffffffffffffffffffffffff", Size: 100, Seeders: "10"}
	searcher := func(results ...nyaa.TorrentResult) nyaaSearcher {
		return nyaaSearcher{
			searchAnime:         func(anilist.Title, []string, []int) []nyaa.TorrentResult { return nil },
			searchSingleEpisode: func(anilist.AiringNode, anilist.Media, int) []nyaa.TorrentResult { return results },
			searchMovie:         func(anilist.Title, bool) []nyaa.TorrentResult { return nil },
		}
	}
	pack := nyaa.TorrentResult{Name: "[Group] Show (01-03)", MagnetLink: directMagnet, Size: 300, Seeders: "50", IsBatch: true}

	result := processAnimeEpisodes(configs, backend, anime, backend.List(), saved, nil, map[files.EpisodeKey]bool{}, searcher(pack), nil, shokoOwned{})
	if len(result.newEpisodes) != 0 || len(result.issues) != 0 {
		t.Fatalf("only the unticked torrent: new %+v, issues %+v; want neither", result.newEpisodes, result.issues)
	}

	result = processAnimeEpisodes(configs, backend, anime, backend.List(), saved, nil, map[files.EpisodeKey]bool{}, searcher(pack, other), nil, shokoOwned{})
	if len(result.newEpisodes) != 1 {
		t.Fatalf("new episodes = %+v, want episode 2 from the other release", result.newEpisodes)
	}
	got := result.newEpisodes[0]
	if got.EpisodeNumber != 2 || got.IsBatch || got.BatchEnd != 0 || got.EpisodeHash == directHash {
		t.Fatalf("record = %+v; want episode 2, a single episode, not the unticked pack", got)
	}
}
