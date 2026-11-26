package daemon

import (
	"context"
	"slices"
	"testing"
	"time"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/torrents"
)

const (
	blDownloading = "1111111111111111111111111111111111111111"
	blQueued      = "2222222222222222222222222222222222222222"
	blDone        = "3333333333333333333333333333333333333333"
	blUserPaused  = "4444444444444444444444444444444444444444"
	blSharedPack  = "5555555555555555555555555555555555555555"
)

// TestBlacklistPausesOnlyTheAnimesUnfinishedTorrents pins what blacklisting pauses: the anime's own
// unfinished torrents, not a finished one (it seeds on), not one the user had paused (lifting must
// not resume it), and not a pack another anime also has episodes in. Lifting resumes exactly those.
func TestBlacklistPausesOnlyTheAnimesUnfinishedTorrents(t *testing.T) {
	fm := tempFileManager(t)
	if err := fm.SaveEpisodesToFile([]files.EpisodeStruct{
		{AnimeID: 7, EpisodeNumber: 1, EpisodeHash: blDownloading},
		{AnimeID: 7, EpisodeNumber: 2, EpisodeHash: blQueued},
		{AnimeID: 7, EpisodeNumber: 3, EpisodeHash: blDone},
		{AnimeID: 7, EpisodeNumber: 4, EpisodeHash: blUserPaused},
		{AnimeID: 7, EpisodeNumber: 5, EpisodeHash: blSharedPack},
		{AnimeID: 8, EpisodeNumber: 1, EpisodeHash: blSharedPack},
	}); err != nil {
		t.Fatal(err)
	}
	backend := torrents.NewFakeBackend()
	for _, h := range []string{blDownloading, blQueued, blSharedPack} {
		backend.NextHash = h
		if _, err := backend.Add("magnet:?xt=urn:btih:" + h); err != nil {
			t.Fatal(err)
		}
	}
	backend.AddCompleted(blDone, "/data")
	backend.AddPaused(blUserPaused, "paused by the user", 1, 10, false)

	b, err := BlacklistAnime(fm, backend, 7, BlacklistInfo{Name: "Witch", Format: "TV"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{blDownloading, blQueued}; !slices.Equal(b.Paused, want) {
		t.Fatalf("paused = %v, want %v", b.Paused, want)
	}
	for hash, want := range map[string]string{blDownloading: "stopped", blQueued: "stopped", blSharedPack: "downloading", blUserPaused: "stopped"} {
		if got, _ := backend.Get(hash); got.Status != want {
			t.Errorf("%s: status %q, want %q", hash[:4], got.Status, want)
		}
	}
	saved, _ := fm.LoadAnimeSettings(7)
	if saved.Blacklisted == nil || saved.Blacklisted.Name != "Witch" {
		t.Fatalf("settings = %+v, want the blacklisting saved", saved)
	}

	// Blacklisting again keeps the first paused list.
	if again, _ := BlacklistAnime(fm, backend, 7, BlacklistInfo{}, time.Now()); !slices.Equal(again.Paused, b.Paused) {
		t.Errorf("second blacklisting changed paused to %v", again.Paused)
	}

	lifted, err := LiftBlacklist(fm, backend, 7)
	if err != nil || !lifted {
		t.Fatalf("lift = %v, %v", lifted, err)
	}
	for hash, want := range map[string]string{blDownloading: "downloading", blQueued: "downloading", blUserPaused: "stopped"} {
		if got, _ := backend.Get(hash); got.Status != want {
			t.Errorf("after lifting, %s: status %q, want %q", hash[:4], got.Status, want)
		}
	}
	if saved, _ := fm.LoadAnimeSettings(7); saved.Blacklisted != nil {
		t.Error("the blacklisting is still saved after lifting")
	}
	if lifted, _ := LiftBlacklist(fm, backend, 7); lifted {
		t.Error("lifting an anime not on the blacklist reported true")
	}
}

// TestHandleSavedEpisodes_AppBlacklistKeepsFiles pins that an anime on the app's blacklist keeps its
// files although the pass skipped it: its episodes are not "checked", so without the guard
// identifyEpisodesNotInWatching would delete them as off-list (decisions/155).
func TestHandleSavedEpisodes_AppBlacklistKeepsFiles(t *testing.T) {
	const hash = "blacklisted-anime-torrent"
	saved := []files.EpisodeStruct{{EpisodeNumber: 1, AnimeID: 300, EpisodeHash: hash}}
	configs := &files.Config{DeleteWatchedEpisodes: true, AutoDeleteDownloads: true}

	backend := fakeWithTorrents(hash)
	handleSavedEpisodes(&mockFileManagerForEpisodes{}, configs, backend, testLibrarian(), handleEpisodesData{
		savedEpisodes: saved,
		blacklisted:   map[int]bool{300: true},
	})
	if _, ok := backend.Get(hash); !ok {
		t.Fatal("the blacklisted anime's torrent was deleted")
	}

	// Control: off the blacklist, the same off-list episode goes.
	handleSavedEpisodes(&mockFileManagerForEpisodes{}, configs, backend, testLibrarian(), handleEpisodesData{savedEpisodes: saved})
	if _, ok := backend.Get(hash); ok {
		t.Fatal("control: the off-list episode was kept without the blacklist")
	}
}

// TestPassSkipsBlacklistedAnime pins the filters the pass applies: a blacklisted anime leaves the
// list it processes, the standalone ids it looks up and the on-disk ids whose list status it asks.
func TestPassSkipsBlacklistedAnime(t *testing.T) {
	skip := map[int]bool{2: true}
	animes := withoutBlacklistedAnimes([]anilist.MediaList{{Media: anilist.Media{Id: 1}}, {Media: anilist.Media{Id: 2}}}, skip)
	if len(animes) != 1 || animes[0].Media.Id != 1 {
		t.Errorf("animes = %+v, want only 1", animes)
	}
	if ids := withoutBlacklistedIDs([]int{1, 2, 3}, skip); !slices.Equal(ids, []int{1, 3}) {
		t.Errorf("ids = %v, want [1 3]", ids)
	}
	keys := withoutAnimes([]files.EpisodeKey{{AnimeID: 1, Episode: 1}, {AnimeID: 2, Episode: 1}}, skip)
	if len(keys) != 1 || keys[0].AnimeID != 1 {
		t.Errorf("keys = %v, want only anime 1's", keys)
	}
	all := map[int]files.AnimeSettings{2: {Blacklisted: &files.Blacklisting{}}, 3: {Progress: 4}}
	if got := files.BlacklistedIDs(all); len(got) != 1 || !got[2] {
		t.Errorf("BlacklistedIDs = %v, want {2}", got)
	}
}

// TestAnimeVerification_SkipsBlacklistedAnime runs a whole pass: the one anime on the list has an
// aired episode and Nyaa finds nothing, so a pass that looks at it reports "no torrent found". On
// the app's blacklist, the pass doesn't look at it and reports nothing.
func TestAnimeVerification_SkipsBlacklistedAnime(t *testing.T) {
	reportPassAniList(t)
	emptyNyaa(t)

	run := func(settings map[int]files.AnimeSettings) CheckReport {
		fm := &lifecycleFM{configs: reportPassConfig(t)}
		fm.settings = settings
		state := NewState()
		AnimeVerification(context.Background(), fm, state, nil, torrents.NewFakeBackend(), testLibrarian())
		return state.GetLastCheckReport()
	}

	if report := run(nil); len(report.Problems) == 0 {
		t.Fatalf("control: the pass reported nothing for the anime, got %+v", report)
	}
	report := run(map[int]files.AnimeSettings{900: {Blacklisted: &files.Blacklisting{At: time.Now()}}})
	if report.FinishedAt.IsZero() {
		t.Fatal("the pass did not finish")
	}
	if len(report.Problems) != 0 {
		t.Fatalf("the blacklisted anime was checked: %+v", report.Problems)
	}
}

// TestKeepBlacklistedPausedCatchesRestartedTorrents: a blacklisted anime's torrent that runs again
// after the click (the app had stopped it, or a running pass added it) is paused at the next pass and
// recorded, so lifting resumes it; one the user paused, and another anime's, are left alone.
func TestKeepBlacklistedPausedCatchesRestartedTorrents(t *testing.T) {
	fm := tempFileManager(t)
	saved := []files.EpisodeStruct{
		{AnimeID: 7, EpisodeNumber: 1, EpisodeHash: blDownloading},
		{AnimeID: 7, EpisodeNumber: 2, EpisodeHash: blUserPaused},
		{AnimeID: 8, EpisodeNumber: 1, EpisodeHash: blQueued},
	}
	if err := fm.SaveAnimeSettings(7, files.AnimeSettings{Blacklisted: &files.Blacklisting{At: time.Now(), Paused: []string{blDone}}}); err != nil {
		t.Fatal(err)
	}
	backend := torrents.NewFakeBackend()
	for _, h := range []string{blDownloading, blQueued} {
		backend.NextHash = h
		if _, err := backend.Add("magnet:?xt=urn:btih:" + h); err != nil {
			t.Fatal(err)
		}
	}
	backend.AddPaused(blUserPaused, "paused by the user", 1, 10, false)

	keepBlacklistedPaused(fm, backend, saved, map[int]bool{7: true})

	if got, _ := backend.Get(blDownloading); got.Status != "stopped" {
		t.Errorf("the blacklisted anime's running torrent: status %q, want stopped", got.Status)
	}
	if got, _ := backend.Get(blQueued); got.Status != "downloading" {
		t.Errorf("another anime's torrent: status %q, want downloading", got.Status)
	}
	s, _ := fm.LoadAnimeSettings(7)
	if want := []string{blDone, blDownloading}; !slices.Equal(s.Blacklisted.Paused, want) {
		t.Errorf("paused = %v, want %v (the user's own pause not recorded)", s.Blacklisted.Paused, want)
	}
}

// TestAnimeVerification_PausesBlacklistedTorrentRunningAgain pins that the pass itself calls
// keepBlacklistedPaused: a blacklisted anime's torrent found running at the start of a pass is paused.
func TestAnimeVerification_PausesBlacklistedTorrentRunningAgain(t *testing.T) {
	reportPassAniList(t)
	emptyNyaa(t)

	fm := &lifecycleFM{configs: reportPassConfig(t), saved: []files.EpisodeStruct{{AnimeID: 900, EpisodeNumber: 1, EpisodeHash: blDownloading}}}
	fm.settings = map[int]files.AnimeSettings{900: {Blacklisted: &files.Blacklisting{At: time.Now()}}}
	backend := torrents.NewFakeBackend()
	backend.NextHash = blDownloading
	if _, err := backend.Add("magnet:?xt=urn:btih:" + blDownloading); err != nil {
		t.Fatal(err)
	}

	AnimeVerification(context.Background(), fm, NewState(), nil, backend, testLibrarian())

	got, ok := backend.Get(blDownloading)
	if !ok {
		t.Fatal("the blacklisted anime's torrent was removed")
	}
	if got.Status != "stopped" {
		t.Fatalf("status %q, want stopped", got.Status)
	}
}
