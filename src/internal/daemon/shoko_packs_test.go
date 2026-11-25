package daemon

import (
	"os"
	"slices"
	"testing"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/shoko"
	"AutoAnimeDownloader/src/internal/torrents"
)

// packInLibrary places a finished 01–03 pack of anime 3 and returns its records (the pack was
// chosen for episode 3) and the three files on disk.
func packInLibrary(t *testing.T, configs *files.Config, backend *torrents.FakeBackend, finished bool) ([]files.EpisodeStruct, []string) {
	t.Helper()
	if err := registerPlacement(configs, backend, directMagnet, torrentTarget{anime: titledAnime(3, "Show", anilist.MediaFormatTV), batch: true}); err != nil {
		t.Fatal(err)
	}
	p, err := backend.PlaceFiles(directHash, []string{"Pack/Show - 01.mkv", "Pack/Show - 02.mkv", "Pack/Show - 03.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range p.Files {
		if err := os.MkdirAll(dirOf(f.Path), 0755); err != nil {
			t.Fatal(err)
		}
		// Unfinished, the files are still ".part" (incomplete_files "part", the default).
		name := f.Path
		if !finished {
			name += ".part"
		}
		if err := os.WriteFile(name, []byte("video"), 0644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, name)
	}
	backend.AddCompleted(directHash, p.SeriesDir)
	if finished {
		if err := backend.FinalizePlacement(directHash); err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(paths)
	rec := files.EpisodeStruct{AnimeID: 3, EpisodeNumber: 3, EpisodeHash: directHash, IsBatch: true, BatchStart: 1, BatchEnd: 3, AnimeTotalEpisodes: 3}
	if finished {
		rec.LibraryPaths = paths
	}
	return []files.EpisodeStruct{rec}, paths
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// "Remove the unneeded episodes" (decisions/153): the pack's files of episodes 1–2, which Shoko
// has, are skipped and deleted; episode 3, the one the pack was chosen for, stays. The built-in
// client can't seed with files missing, so its torrent leaves the client first; qBittorrent keeps
// seeding. An anime that keeps them, or an episode the app has a record of, is left alone, and an
// unfinished pack is only marked.
func TestDropShokoPackFiles(t *testing.T) {
	owned := map[int]map[int]shoko.File{3: {1: {ID: 1}, 2: {ID: 2}}}
	remove := map[int]files.AnimeSettings{3: {ShokoPackEpisodes: files.ShokoPackRemove}}

	t.Run("built-in client", func(t *testing.T) {
		lib := t.TempDir()
		configs := directConfig(lib)
		backend := torrents.NewFakeBackend()
		backend.LibraryRoot = lib
		records, paths := packInLibrary(t, configs, backend, true)
		fm := &orchestrationFM{saved: records, configs: configs}
		dropShokoPackFiles(configs, backend, fm, fm.saved, owned, remove)
		if exists(paths[0]) || exists(paths[1]) || !exists(paths[2]) {
			t.Fatalf("want 01 and 02 deleted and 03 kept: %v %v %v", exists(paths[0]), exists(paths[1]), exists(paths[2]))
		}
		if keep, removed := backend.RemovedKeepData[directHash]; !removed || !keep {
			t.Errorf("rain must release the torrent with the rest kept: removed=%v keepData=%v", removed, keep)
		}
		if !fm.saved[0].TorrentReleased || !slices.Equal(fm.saved[0].LibraryPaths, paths[2:]) {
			t.Errorf("record = %+v; want released, library paths only 03", fm.saved[0])
		}
		if p, _ := backend.Placement(directHash); PlacedFilesMissing(p) {
			t.Error("deleted skipped files must not read as missing")
		}
	})

	t.Run("qBittorrent keeps seeding", func(t *testing.T) {
		lib := t.TempDir()
		configs := directConfig(lib)
		configs.TorrentClient = files.TorrentClientQBittorrent
		backend := torrents.NewFakeBackend()
		backend.LibraryRoot = lib
		records, paths := packInLibrary(t, configs, backend, true)
		fm := &orchestrationFM{saved: records, configs: configs}
		dropShokoPackFiles(configs, backend, fm, fm.saved, owned, remove)
		if exists(paths[0]) || exists(paths[1]) || !exists(paths[2]) {
			t.Fatal("want 01 and 02 deleted and 03 kept")
		}
		if _, inClient := backend.Get(directHash); !inClient || fm.saved[0].TorrentReleased {
			t.Error("qBittorrent seeds the rest: the torrent must stay")
		}
		if !slices.Contains(backend.SkippedCalls, directHash) {
			t.Error("the files must be skipped through the backend (priority 0)")
		}
		dropShokoPackFiles(configs, backend, fm, fm.saved, owned, remove)
		if len(backend.SkippedCalls) != 1 {
			t.Errorf("a pass with nothing new must not skip again: %d calls", len(backend.SkippedCalls))
		}
	})

	t.Run("kept, recorded or unfinished", func(t *testing.T) {
		for name, tc := range map[string]struct {
			settings map[int]files.AnimeSettings
			full     bool
			extra    []files.EpisodeStruct
			finished bool
			want     []bool // 01, 02, 03 still on disk
			skipped  bool
		}{
			"the anime keeps them":          {settings: map[int]files.AnimeSettings{3: {ShokoPackEpisodes: files.ShokoPackDownload}}, full: false, finished: true, want: []bool{true, true, true}},
			"the global default keeps them": {settings: nil, full: true, finished: true, want: []bool{true, true, true}},
			"the global default removes":    {settings: nil, full: false, finished: true, want: []bool{false, false, true}, skipped: true},
			"episode 2 is the app's own":    {settings: remove, full: true, finished: true, extra: []files.EpisodeStruct{{AnimeID: 3, EpisodeNumber: 2, EpisodeHash: "other"}}, want: []bool{false, true, true}, skipped: true},
			"the pack is still downloading": {settings: remove, full: true, finished: false, want: []bool{true, true, true}, skipped: true},
		} {
			t.Run(name, func(t *testing.T) {
				lib := t.TempDir()
				configs := directConfig(lib)
				configs.ShokoFullPacks = tc.full
				backend := torrents.NewFakeBackend()
				backend.LibraryRoot = lib
				records, paths := packInLibrary(t, configs, backend, tc.finished)
				fm := &orchestrationFM{saved: append(records, tc.extra...), configs: configs}
				dropShokoPackFiles(configs, backend, fm, fm.saved, owned, tc.settings)
				for i, want := range tc.want {
					if exists(paths[i]) != want {
						t.Errorf("file %d on disk = %v, want %v", i+1, !want, want)
					}
				}
				if got := slices.Contains(backend.SkippedCalls, directHash); got != tc.skipped {
					t.Errorf("skipped = %v, want %v", got, tc.skipped)
				}
				if _, inClient := backend.Get(directHash); !inClient && tc.finished && tc.want[0] {
					t.Error("nothing removed: the torrent must stay")
				}
			})
		}
	})

	t.Run("handed to Shoko: skipped, never deleted", func(t *testing.T) {
		lib := t.TempDir()
		configs := directConfig(lib)
		backend := torrents.NewFakeBackend()
		backend.LibraryRoot = lib
		records, paths := packInLibrary(t, configs, backend, true)
		records[0].HandedToShoko = true
		fm := &orchestrationFM{saved: records, configs: configs}
		dropShokoPackFiles(configs, backend, fm, fm.saved, owned, remove)
		for i, p := range paths {
			if !exists(p) {
				t.Errorf("file %d was handed to Shoko and must stay", i+1)
			}
		}
		if _, inClient := backend.Get(directHash); !inClient {
			t.Error("nothing deleted: the torrent must stay")
		}
		if !slices.Contains(backend.SkippedCalls, directHash) {
			t.Error("the skip still applies (qBittorrent never writes them)")
		}
	})
}
