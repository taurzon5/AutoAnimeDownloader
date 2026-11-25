package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/torrents"
)

const directHash = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
const directMagnet = "magnet:?xt=urn:btih:" + directHash

func directConfig(lib string) *files.Config {
	c := files.DefaultConfig()
	c.CompletedAnimePath = lib
	c.LibraryMode = files.LibraryModeDirect
	return c
}

func titledAnime(id int, english string, format anilist.MediaFormat) anilist.MediaList {
	title := english
	return anilist.MediaList{Id: id, Media: anilist.Media{Id: id, Format: format, Title: anilist.Title{English: &title}}}
}

// Mushoku Tensei-like chain: S1 (23), S2 (12), S2 Part 2 (12).
func mushokuChain() *anilist.Series {
	return &anilist.Series{Key: 1, Offset: 35, Chain: []anilist.ChainEntry{
		{ID: 1, English: "Mushoku Tensei: Jobless Reincarnation", Episodes: 23},
		{ID: 2, English: "Mushoku Tensei: Jobless Reincarnation Season 2", Episodes: 12},
		{ID: 3, English: "Mushoku Tensei: Jobless Reincarnation Season 2 Part 2", Episodes: 12},
	}}
}

func TestPlacementRequest_EpisodeOfASequelGoesToTheSeriesFolder(t *testing.T) {
	lib := "/lib"
	anime := titledAnime(3, "Mushoku Tensei: Jobless Reincarnation Season 2 Part 2", anilist.MediaFormatTV)
	req := placementRequest(directConfig(lib), directHash, torrentTarget{anime: anime, series: mushokuChain(), episode: 1})

	if req.SeriesDir != "Mushoku Tensei Jobless Reincarnation" {
		t.Errorf("the series folder must be named after the chain root, got %q", req.SeriesDir)
	}
	if req.Kind != torrents.PlacementEpisode || req.Season != 2 {
		t.Errorf("a Season 2 Part 2 episode goes to Season 02, got kind=%s season=%d", req.Kind, req.Season)
	}
	if req.SeriesID != 1 || req.SeriesName != "Mushoku Tensei: Jobless Reincarnation" {
		t.Errorf("the nfo must carry the root: id=%d name=%q", req.SeriesID, req.SeriesName)
	}
	if !req.PartFiles {
		t.Error("incomplete_files defaults to part")
	}
	if req.RenameVideo != "" {
		t.Errorf("no rename without rename_files_for_jellyfin, got %q", req.RenameVideo)
	}
}

func TestPlacementRequest_SplitCourNumbering(t *testing.T) {
	anime := titledAnime(3, "Mushoku Tensei: Jobless Reincarnation Season 2 Part 2", anilist.MediaFormatTV)
	target := torrentTarget{anime: anime, series: mushokuChain(), episode: 1}

	configs := directConfig("/lib")
	configs.RenameFilesForJellyfin = true
	if got, want := placementRequest(configs, directHash, target).RenameVideo, "Mushoku Tensei Jobless Reincarnation - S02E13"; got != want {
		t.Errorf("season numbering continues after Part 1's 12: got %q, want %q", got, want)
	}

	configs.EpisodeNumbering = files.EpisodeNumberingEntry
	if got, want := placementRequest(configs, directHash, target).RenameVideo, "Mushoku Tensei Jobless Reincarnation - S02E01 (Part 2)"; got != want {
		t.Errorf("entry numbering keeps the entry's number and tags the part: got %q, want %q", got, want)
	}
}

// The real Mushoku Tensei chain, with AniList's romaji titles: the second half of season 1 has no
// season marker, only "Part 2" — a part, not a new season (decisions/010). It must share Season 01
// with the first half, and "II" must not be counted as following two seasons.
func TestPlacementRequest_RealMushokuChainKeepsPartsInTheirSeason(t *testing.T) {
	chain := []anilist.ChainEntry{
		{ID: 108465, Romaji: "Mushoku Tensei: Isekai Ittara Honki Dasu", Episodes: 11},
		{ID: 127720, Romaji: "Mushoku Tensei: Isekai Ittara Honki Dasu Part 2", Episodes: 12},
		{ID: 146065, Romaji: "Mushoku Tensei II: Isekai Ittara Honki Dasu", Episodes: 12},
		{ID: 166873, Romaji: "Mushoku Tensei II: Isekai Ittara Honki Dasu Part 2", Episodes: 12},
	}
	configs := directConfig("/lib")
	configs.RenameFilesForJellyfin = true
	cases := []struct {
		upTo       int
		wantSeason int
		wantName   string
	}{
		{1, 1, "Mushoku Tensei Isekai Ittara Honki Dasu - S01E01"},
		{2, 1, "Mushoku Tensei Isekai Ittara Honki Dasu - S01E12"},
		{3, 2, "Mushoku Tensei Isekai Ittara Honki Dasu - S02E01"},
		{4, 2, "Mushoku Tensei Isekai Ittara Honki Dasu - S02E13"},
	}
	for _, c := range cases {
		self := chain[c.upTo-1]
		romaji := self.Romaji
		anime := anilist.MediaList{Id: self.ID, Media: anilist.Media{Id: self.ID, Format: anilist.MediaFormatTV, Title: anilist.Title{Romaji: &romaji}}}
		series := &anilist.Series{Key: 108465, Chain: chain[:c.upTo]}
		req := placementRequest(configs, directHash, torrentTarget{anime: anime, series: series, episode: 1})
		if req.Season != c.wantSeason || req.RenameVideo != c.wantName {
			t.Errorf("%s E01: got Season %d %q, want Season %d %q", romaji, req.Season, req.RenameVideo, c.wantSeason, c.wantName)
		}
	}
}

func TestPlacementRequest_UnmarkedSequelUsesItsChainPosition(t *testing.T) {
	anime := titledAnime(2, "Frieren Next", anilist.MediaFormatTV)
	series := &anilist.Series{Key: 1, Chain: []anilist.ChainEntry{{ID: 1, English: "Frieren"}, {ID: 2, English: "Frieren Next"}}}
	if got := placementRequest(directConfig("/lib"), directHash, torrentTarget{anime: anime, series: series, episode: 3}).Season; got != 2 {
		t.Errorf("an unmarked second entry is season 2, got %d", got)
	}
}

func TestPlacementRequest_UnknownChainFallsBackToTheAnime(t *testing.T) {
	anime := titledAnime(9, "Bocchi the Rock!", anilist.MediaFormatTV)
	req := placementRequest(directConfig("/lib"), directHash, torrentTarget{anime: anime, episode: 2})
	if req.SeriesDir != "Bocchi the Rock!" || req.Season != 1 || req.SeriesID != 9 {
		t.Errorf("no chain: the anime names the folder, season 1, its own id; got %+v", req)
	}
}

func TestPlacementRequest_PackAndMovieKinds(t *testing.T) {
	configs := directConfig("/lib")
	pack := placementRequest(configs, directHash, torrentTarget{anime: titledAnime(1, "Kaguya", anilist.MediaFormatTV), batch: true})
	if pack.Kind != torrents.PlacementPack {
		t.Errorf("a batch is a pack, got %s", pack.Kind)
	}
	movie := placementRequest(configs, directHash, torrentTarget{anime: titledAnime(5, "Mugen Train", anilist.MediaFormatMovie), batch: true})
	if movie.Kind != torrents.PlacementMovie {
		t.Errorf("a MOVIE is a movie even as a batch, got %s", movie.Kind)
	}
	configs.IncompleteFiles = files.IncompleteFilesInPlace
	if placementRequest(configs, directHash, torrentTarget{anime: titledAnime(1, "Kaguya", anilist.MediaFormatTV)}).PartFiles {
		t.Error("in_place must not write part files")
	}
}

func TestRegisterPlacement_OnlyInDirectMode(t *testing.T) {
	backend := torrents.NewFakeBackend()
	hardlink := files.DefaultConfig()
	hardlink.CompletedAnimePath = "/lib"
	if err := registerPlacement(hardlink, backend, directMagnet, torrentTarget{anime: titledAnime(1, "A", anilist.MediaFormatTV), episode: 1}, files.DownloadRoot{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := backend.Placement(directHash); ok {
		t.Fatal("hardlink mode must not register placements")
	}
	if err := registerPlacement(directConfig("/lib"), backend, directMagnet, torrentTarget{anime: titledAnime(1, "A", anilist.MediaFormatTV), episode: 1}, files.DownloadRoot{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := backend.Placement(directHash); !ok {
		t.Fatal("direct mode must register the placement before the add")
	}
}

// organize in direct mode: no hardlink, the .part names become final, LibraryPaths are the
// placed video files, and tvshow.nfo goes in the series folder.
func TestOrganizeTorrent_DirectModeFinalizesAndRecordsThePlacedFiles(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	anime := titledAnime(3, "Show Season 2", anilist.MediaFormatTV)
	if err := registerPlacement(configs, backend, directMagnet, torrentTarget{anime: anime, episode: 5}, files.DownloadRoot{}); err != nil {
		t.Fatal(err)
	}
	p, err := backend.PlaceFiles(directHash, []string{"ep05.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	// What rain wrote: the .part file.
	if err := os.MkdirAll(filepath.Dir(p.Files[0].Path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Files[0].Path+".part", []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	backend.AddCompleted(directHash, p.SeriesDir)

	fm := &orchestrationFM{saved: []files.EpisodeStruct{{AnimeID: 3, EpisodeHash: directHash, AnimeName: "Show Season 2", EpisodeNumber: 5}}, configs: configs}
	if ok := organizeTorrent(directHash, backend, files.NewLibrarian(files.NewOSFileSystem()), fm, configs, false); !ok {
		t.Fatal("organizeTorrent should succeed")
	}

	final := filepath.Join(lib, "Show Season 2", "Season 02", "ep05.mkv")
	if _, err := os.Stat(final); err != nil {
		t.Fatalf("the file must have its final name: %v", err)
	}
	if got := fm.saved[0].LibraryPaths; len(got) != 1 || got[0] != final {
		t.Fatalf("LibraryPaths must be the placed file, got %v", got)
	}
	if !fm.saved[0].DirectLibrary {
		t.Error("the record must be marked direct_library so the nfo backfill leaves it alone")
	}
	if _, err := os.Stat(filepath.Join(lib, "Show Season 2", "tvshow.nfo")); err != nil {
		t.Fatalf("tvshow.nfo must be in the series folder: %v", err)
	}
	if entries, _ := os.ReadDir(lib); len(entries) != 1 {
		t.Fatalf("nothing but the series folder may appear in the library, got %d entries", len(entries))
	}
}

// Deleting a direct-mode episode deletes the torrent's own files through the backend, never a
// library "hardlink" first.
func TestRemoveEpisodesAndLinks_DirectModeDeletesThroughTheTorrent(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
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
	if _, err := backend.Add(directMagnet); err != nil {
		t.Fatal(err)
	}

	saved := []files.EpisodeStruct{{AnimeID: 3, EpisodeNumber: 1, EpisodeHash: directHash, LibraryPaths: []string{final}}}
	spy := &spyLibrarian{}
	fm := &orchestrationFM{saved: saved, configs: configs}
	if err := removeEpisodesAndLinks(fm, backend, spy, []files.EpisodeKey{{AnimeID: 3, Episode: 1}}, saved, false, false, map[int]bool{}); err != nil {
		t.Fatal(err)
	}
	if spy.called {
		t.Error("direct mode must not remove the file as a library hardlink")
	}
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Errorf("removing the torrent must delete its file: %v", err)
	}
	if _, ok := backend.Placement(directHash); ok {
		t.Error("the placement must be forgotten with the files")
	}
}

func TestWithoutKeptForever(t *testing.T) {
	configs := directConfig("/lib")
	backend := torrents.NewFakeBackend()
	if err := registerPlacement(configs, backend, directMagnet, torrentTarget{anime: titledAnime(3, "Show", anilist.MediaFormatTV), episode: 1}, files.DownloadRoot{}); err != nil {
		t.Fatal(err)
	}
	const legacyHash = "1111111111111111111111111111111111111111"
	saved := []files.EpisodeStruct{
		{AnimeID: 3, EpisodeNumber: 1, EpisodeHash: directHash},
		{AnimeID: 3, EpisodeNumber: 2, EpisodeHash: legacyHash},
	}
	keys := []files.EpisodeKey{{AnimeID: 3, Episode: 1}, {AnimeID: 3, Episode: 2}}

	got := withoutKeptForever(configs, backend, keys, saved)
	if len(got) != 1 || got[0].Episode != 2 {
		t.Fatalf("keep forever spares direct-mode torrents only, got %v", got)
	}
	configs.AutoDeleteDownloads = true
	if got := withoutKeptForever(configs, backend, keys, saved); len(got) != 2 {
		t.Fatalf("auto_delete_downloads deletes as usual, got %v", got)
	}
}

func TestKeepForeverLiftsTheEpisodeLimit(t *testing.T) {
	configs := directConfig("/lib")
	configs.MaxEpisodesPerAnime = 12
	episodes := make([]anilist.AiringNode, 30)
	if got := effectiveMax(configs, episodes); got != len(episodes)+1 {
		t.Errorf("keep forever must lift the limit, got %d", got)
	}
	configs.AutoDeleteDownloads = true
	if got := effectiveMax(configs, episodes); got != 12 {
		t.Errorf("with auto delete the limit holds, got %d", got)
	}
}

// placedOnDisk registers a direct-mode placement for hash, plans it and writes its file, the
// state a torrent leaves after rain wrote it.
func placedOnDisk(t *testing.T, backend *torrents.FakeBackend, configs *files.Config, hash string, episode int) string {
	t.Helper()
	magnet := "magnet:?xt=urn:btih:" + hash
	if err := registerPlacement(configs, backend, magnet, torrentTarget{anime: titledAnime(3, "Show", anilist.MediaFormatTV), episode: episode}, files.DownloadRoot{}); err != nil {
		t.Fatal(err)
	}
	p, err := backend.PlaceFiles(hash, []string{fmt.Sprintf("ep%02d.mkv", episode)})
	if err != nil {
		t.Fatal(err)
	}
	path := p.Files[0].Path
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPruneOrphanPlacements(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib

	const (
		orphan     = "1000000000000000000000000000000000000001"
		referenced = "2000000000000000000000000000000000000002"
		young      = "3000000000000000000000000000000000000003"
		live       = "4000000000000000000000000000000000000004"
	)
	orphanFile := placedOnDisk(t, backend, configs, orphan, 1)
	referencedFile := placedOnDisk(t, backend, configs, referenced, 2)
	youngFile := placedOnDisk(t, backend, configs, young, 3)
	liveFile := placedOnDisk(t, backend, configs, live, 4)
	for _, h := range []string{orphan, referenced, live} {
		backend.AgePlacement(h, 2*orphanPlacementGrace)
	}
	if _, err := backend.Add("magnet:?xt=urn:btih:" + live); err != nil {
		t.Fatal(err)
	}
	saved := []files.EpisodeStruct{{AnimeID: 3, EpisodeNumber: 2, EpisodeHash: referenced}}

	pruneOrphanPlacements(backend, saved, time.Now())

	if _, err := os.Stat(orphanFile); !os.IsNotExist(err) {
		t.Errorf("an abandoned download's file must go: %v", err)
	}
	if _, ok := backend.Placement(orphan); ok {
		t.Error("an abandoned placement must be forgotten")
	}
	for what, path := range map[string]string{"a record's": referencedFile, "a young placement's": youngFile, "a live torrent's": liveFile} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s file must stay: %v", what, err)
		}
	}
}

// A torrent removed by hand with keep_data leaves its files as the user's: the placement is marked
// kept first, so the orphan sweep only ever forgets it, never deletes the files.
func TestRemoveTorrentWithEpisodes_KeepDataKeepsTheFilesFromTheSweep(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	path := placedOnDisk(t, backend, configs, directHash, 1)
	if _, err := backend.Add(directMagnet); err != nil {
		t.Fatal(err)
	}
	fm := &failingFM{saved: []files.EpisodeStruct{{AnimeID: 3, EpisodeNumber: 1, EpisodeHash: directHash, LibraryPaths: []string{path}}}}

	if err := RemoveTorrentWithEpisodes(fm, backend, &spyLibrarian{}, directHash, RemoveTorrentOptions{KeepData: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("keep_data must keep the file: %v", err)
	}
	if p, ok := backend.Placement(directHash); !ok || !p.Kept {
		t.Fatal("keep_data must mark the placement kept before the removal")
	}
	backend.AgePlacement(directHash, 2*orphanPlacementGrace)
	pruneOrphanPlacements(backend, nil, time.Now())
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the sweep must never touch a file kept on purpose: %v", err)
	}
	if p, ok := backend.Placement(directHash); !ok || !p.Kept {
		t.Fatal("a kept placement is never forgotten: a later re-add must resume into the files")
	}
}

// AniList edits titles. A new season of a series that already has a folder goes into that folder,
// not a second one named after the edited title.
func TestRegisterPlacement_PinsTheSeriesFolder(t *testing.T) {
	configs := directConfig("/lib")
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = "/lib"
	series := &anilist.Series{Key: 1, Chain: []anilist.ChainEntry{{ID: 1, English: "Old Title"}}}
	if err := registerPlacement(configs, backend, directMagnet, torrentTarget{anime: titledAnime(1, "Old Title", anilist.MediaFormatTV), series: series, episode: 1}, files.DownloadRoot{}); err != nil {
		t.Fatal(err)
	}

	const s2 = "5000000000000000000000000000000000000005"
	edited := &anilist.Series{Key: 1, Chain: []anilist.ChainEntry{{ID: 1, English: "New Title"}, {ID: 2, English: "New Title Season 2"}}}
	if err := registerPlacement(configs, backend, "magnet:?xt=urn:btih:"+s2, torrentTarget{anime: titledAnime(2, "New Title Season 2", anilist.MediaFormatTV), series: edited, episode: 1}, files.DownloadRoot{}); err != nil {
		t.Fatal(err)
	}
	p, _ := backend.Placement(s2)
	if p.SeriesDir != "/lib/Old Title" {
		t.Fatalf("season 2 must join the series' existing folder, got %q", p.SeriesDir)
	}
}

// A switch back to hardlink mode must not start deleting what keep forever kept: the torrent keeps
// the mode it was added in.
func TestWithoutKeptForever_SurvivesASwitchBackToHardlinkMode(t *testing.T) {
	configs := directConfig("/lib")
	backend := torrents.NewFakeBackend()
	if err := registerPlacement(configs, backend, directMagnet, torrentTarget{anime: titledAnime(3, "Show", anilist.MediaFormatTV), episode: 1}, files.DownloadRoot{}); err != nil {
		t.Fatal(err)
	}
	configs.LibraryMode = files.LibraryModeHardlink
	saved := []files.EpisodeStruct{{AnimeID: 3, EpisodeNumber: 1, EpisodeHash: directHash}}
	if got := withoutKeptForever(configs, backend, []files.EpisodeKey{{AnimeID: 3, Episode: 1}}, saved); len(got) != 0 {
		t.Fatalf("a kept direct-mode torrent must stay kept after the switch, got %v", got)
	}
}

// When marking the files kept fails, nothing is removed: not the torrent, not the records. The
// sweep could otherwise delete the files the user asked to keep.
func TestRemoveTorrentWithEpisodes_KeepDataAbortsWhenTheFilesCannotBeKept(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	path := placedOnDisk(t, backend, configs, directHash, 1)
	if _, err := backend.Add(directMagnet); err != nil {
		t.Fatal(err)
	}
	backend.KeepErr = fmt.Errorf("disk full")
	fm := &failingFM{saved: []files.EpisodeStruct{{AnimeID: 3, EpisodeNumber: 1, EpisodeHash: directHash, LibraryPaths: []string{path}}}}

	err := RemoveTorrentWithEpisodes(fm, backend, &spyLibrarian{}, directHash, RemoveTorrentOptions{KeepData: true, Block: true})
	if err == nil {
		t.Fatal("the failure must reach the caller, not come back as a success that deleted nothing (decisions/074)")
	}
	if _, ok := backend.Get(directHash); !ok {
		t.Error("the torrent must stay in the session")
	}
	if len(fm.deleted) != 0 {
		t.Errorf("the records must stay, deleted %v", fm.deleted)
	}
	if len(fm.blockedEpisodeKeys) != 0 {
		t.Errorf("nothing may be blocked before the keep succeeds, blocked %v", fm.blockedEpisodeKeys)
	}
}

// Kept is permanent: the sweep never clears it, never forgets the placement and never touches its
// files, whether the torrent is in the session (re-added, or being removed right now) or gone.
func TestPruneOrphanPlacements_KeptPlacements(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib

	const inSession = "6000000000000000000000000000000000000006"
	const unused = "7000000000000000000000000000000000000007"
	inSessionPath := placedOnDisk(t, backend, configs, inSession, 1)
	unusedPath := placedOnDisk(t, backend, configs, unused, 2)
	for _, h := range []string{inSession, unused} {
		if err := backend.SetPlacementKept(h, true); err != nil {
			t.Fatal(err)
		}
		backend.AgePlacement(h, 2*orphanPlacementGrace)
	}
	if _, err := backend.Add("magnet:?xt=urn:btih:" + inSession); err != nil {
		t.Fatal(err)
	}

	pruneOrphanPlacements(backend, nil, time.Now())

	if p, ok := backend.Placement(inSession); !ok || !p.Kept {
		t.Errorf("the sweep must leave a kept placement in the session kept, got %+v ok=%v", p, ok)
	}
	if p, ok := backend.Placement(unused); !ok || !p.Kept {
		t.Errorf("a kept placement nothing uses must stay kept, got %+v ok=%v", p, ok)
	}
	for _, path := range []string{inSessionPath, unusedPath} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("kept files must never be deleted: %v", err)
		}
	}
}

// The torrent of a deleted file stops with an error; the failure handler leaves it alone — no
// drop, no re-add, no webhook (decisions/139).
func TestHandleTorrentFailure_LeavesADeletedFilesTorrentAlone(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	path := placedOnDisk(t, backend, configs, directHash, 1)
	if _, err := backend.Add(directMagnet); err != nil {
		t.Fatal(err)
	}
	if err := backend.FinalizePlacement(directHash); err != nil {
		t.Fatal(err)
	}
	if p, _ := backend.Placement(directHash); !p.Completed {
		t.Fatal("an in-place placement must be marked completed by finalize")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	fm := &failingFM{saved: []files.EpisodeStruct{{AnimeID: 3, EpisodeNumber: 1, EpisodeHash: directHash, DirectLibrary: true, LibraryPaths: []string{path}}}}

	HandleTorrentFailure(directHash, torrents.ErrPlacedFileMissing, backend, fm)

	if _, ok := backend.Get(directHash); !ok {
		t.Error("the torrent must stay in the session")
	}
	if _, removed := backend.RemovedKeepData[directHash]; removed {
		t.Error("the torrent must not be removed")
	}
}

// Open refuses ANY missing file of a finished placement, so the check behind the flag, the failure
// handler and organize must cover every file too: a deleted subtitle alone stops the torrent.
func TestPlacedFilesMissing_CoversEveryFile(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	if err := registerPlacement(configs, backend, directMagnet, torrentTarget{anime: titledAnime(3, "Show", anilist.MediaFormatTV), episode: 1}, files.DownloadRoot{}); err != nil {
		t.Fatal(err)
	}
	p, err := backend.PlaceFiles(directHash, []string{"R/ep01.mkv", "R/ep01.ass"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range p.Files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.Path, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := backend.FinalizePlacement(directHash); err != nil {
		t.Fatal(err)
	}
	p, _ = backend.Placement(directHash)
	if PlacedFilesMissing(p) {
		t.Fatal("nothing is missing yet")
	}
	if err := os.Remove(p.Files[1].Path); err != nil { // the subtitle
		t.Fatal(err)
	}
	if !PlacedFilesMissing(p) {
		t.Fatal("a deleted subtitle must count: Open refuses it too")
	}
}

// A finished torrent whose files are gone (the library folder was replaced) is not organized
// again: no LibraryPaths for files that don't exist, no completion webhook.
func TestOrganizeTorrent_LeavesAFinishedTorrentWithMissingFilesAlone(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	path := placedOnDisk(t, backend, configs, directHash, 1)
	if err := backend.FinalizePlacement(directHash); err != nil {
		t.Fatal(err)
	}
	p, _ := backend.Placement(directHash)
	backend.AddCompleted(directHash, p.SeriesDir)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	fm := &orchestrationFM{saved: []files.EpisodeStruct{{AnimeID: 3, EpisodeHash: directHash, AnimeName: "Show", EpisodeNumber: 1}}, configs: configs}

	if ok := organizeTorrent(directHash, backend, files.NewLibrarian(files.NewOSFileSystem()), fm, configs, false); !ok {
		t.Fatal("the job is done: there is nothing to retry")
	}
	if len(fm.upserted) != 0 || len(fm.saved[0].LibraryPaths) != 0 {
		t.Fatalf("no library paths may be recorded for missing files, got %+v", fm.saved[0])
	}
}

func TestLibraryMissingItem_SaysWhatHappensInEachMode(t *testing.T) {
	if got := libraryMissingItem("/lib", 3, false).Text; !strings.Contains(got, "3 episodes will be downloaded again") {
		t.Errorf("hardlink mode re-downloads, got %q", got)
	}
	if got := libraryMissingItem("/lib", 3, true).Text; strings.Contains(got, "will be downloaded again") || !strings.Contains(got, "Redownload missing files") {
		t.Errorf("direct mode does not on its own, and names the way back, got %q", got)
	}
}

// finishedPlacement places one episode of hash on disk, finalizes it and puts its torrent in the
// session as finished, with a file list of the given size. Returns the file's path.
func finishedPlacement(t *testing.T, backend *torrents.FakeBackend, configs *files.Config, hash string, episode int, size int64) string {
	t.Helper()
	path := placedOnDisk(t, backend, configs, hash, episode)
	if err := backend.FinalizePlacement(hash); err != nil {
		t.Fatal(err)
	}
	p, _ := backend.Placement(hash)
	backend.AddCompleted(hash, p.SeriesDir)
	backend.SetFiles(hash, []torrents.FileInfo{{Path: p.Files[0].Torrent, Size: size}})
	return path
}

// The bulk "redownload missing files" (decisions/139) touches only finished torrents whose files
// are gone and that are still in the session, and only the ones the user left ticked.
func TestRedownloadMissingFiles_PicksTheTorrentsWithMissingFiles(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
	configs.MinFreeDiskGB = 0
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib

	const (
		picked   = "a000000000000000000000000000000000000001"
		unticked = "b000000000000000000000000000000000000002"
		intact   = "c000000000000000000000000000000000000003"
		gone     = "d000000000000000000000000000000000000004"
	)
	for i, h := range []string{picked, unticked, intact, gone} {
		path := finishedPlacement(t, backend, configs, h, i+1, 10)
		if h != intact {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := backend.Remove(gone, true); err != nil { // no longer in the session
		t.Fatal(err)
	}

	result, err := RedownloadMissingFiles(configs, backend, nil, []string{strings.ToUpper(picked)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Redownloaded != 1 || result.SkippedNoSpace != 0 {
		t.Fatalf("only the picked torrent must go, got %+v", result)
	}
	if calls := backend.RedownloadCalls(); len(calls) != 1 || calls[0] != picked {
		t.Fatalf("calls = %v, want [%s]", calls, picked)
	}
	if p, _ := backend.Placement(picked); p.Completed {
		t.Error("the redownloaded placement must be reopened")
	}
	if p, _ := backend.Placement(unticked); !p.Completed {
		t.Error("an unticked torrent must be left as it was")
	}

	// No list: every torrent with missing files. The picked one is downloading now (its placement
	// reopened), so only the unticked one is left.
	result, err = RedownloadMissingFiles(configs, backend, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls := backend.RedownloadCalls(); result.Redownloaded != 1 || len(calls) != 2 || calls[1] != unticked {
		t.Fatalf("the second run must take only the unticked torrent, got %+v and calls %v", result, calls)
	}
}

func TestRedownloadMissingFiles_SkipsWhatDoesNotFit(t *testing.T) {
	lib := t.TempDir()
	configs := directConfig(lib)
	configs.MinFreeDiskGB = 1 << 30 // no disk has this much free
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = lib
	path := finishedPlacement(t, backend, configs, directHash, 1, 10)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	result, err := RedownloadMissingFiles(configs, backend, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Redownloaded != 0 || result.SkippedNoSpace != 1 || len(backend.RedownloadCalls()) != 0 {
		t.Fatalf("a torrent that doesn't fit must be skipped, got %+v", result)
	}
	if p, _ := backend.Placement(directHash); !p.Completed {
		t.Error("a skipped torrent must stay finished: nothing may recreate its file")
	}
}
