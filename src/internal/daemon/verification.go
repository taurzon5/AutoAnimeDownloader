package daemon

import (
	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/notifications"
	"AutoAnimeDownloader/src/internal/nyaa"
	"AutoAnimeDownloader/src/internal/torrents"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// animeProcessResult holds the per-anime outputs from processAnimeEpisodes.
type animeProcessResult struct {
	newEpisodes     []files.EpisodeStruct
	checkedEpisodes []files.EpisodeKey
	keysToDelete    []files.EpisodeKey
	// issues are the reasons this anime stopped downloading something. It comes through the channel
	// that already exists on purpose: a *passReport shared between the maxConcurrentAnimes goroutines
	// would need a mutex, and the fan-in already solves that for free.
	issues []Issue
}

// maxConcurrentAnimes limits simultaneous Nyaa HTTP searches to avoid rate limiting.
const maxConcurrentAnimes = 5

// verificationMu serializes the passes. A pass reads episodes.json ONCE at the start and only
// writes at the end (Phase 3), so two simultaneous passes — the loop's and POST /check's, or two
// /check — see the same stale list and download everything again: that's how an anime already
// covered by a pack got standalone torrents of the same episodes, and the second pass's write
// overwrote the pack's records. A concurrent pass is DROPPED, not queued: it would read the same
// state the running pass is already fixing, and the loop's next tick covers whatever is missing.
var verificationMu sync.Mutex

func AnimeVerification(ctx context.Context, fileManager FileManagerInterface, state *State, jobQueue *JobQueue, backend torrents.TorrentBackend, librarian files.Librarian) {
	if !verificationMu.TryLock() {
		logger.Logger.Info().Msg("Verification already running; skipping this trigger")
		return
	}
	defer verificationMu.Unlock()
	passStarted := time.Now()

	// Every pass that ran feeds check_failing — registered before the first abort so all of them
	// count. Not a cancelled one: it clears the error and would read as a recovery.
	var configs *files.Config
	defer func() {
		if ctx.Err() == nil {
			trackCheckFailing(configs, state.GetLastCheckErrorCode(), time.Now())
		}
	}()

	configs, err := fileManager.LoadConfigs()
	if err != nil {
		logger.Logger.Error().Err(err).Stack().Msg("Failed to load configs")
		state.SetLastCheckError(fmt.Errorf("%w: %w", errCauseConfig, err))
		return
	}

	if !isConfigComplete(configs) {
		if !HasDisplay() {
			// Headless (install-system): no browser here, the Config page is opened from another machine.
			logger.Logger.Warn().Str("url", getWebUiURL()).Msg("Missing required configuration: set the library folder on the web UI's Config page")
			state.SetLastCheckError(fmt.Errorf("%w: library folder is missing", errCauseSetup))
			return
		}
		logger.Logger.Warn().Msg("Missing required configuration, opening browser to config page")
		go func() {
			time.Sleep(500 * time.Millisecond)
			webUIURL := getWebUiURL()
			if err := OpenBrowser(webUIURL); err != nil {
				logger.Logger.Warn().Err(err).Msg("Failed to open browser to configuration page")
			}
		}()

		state.SetLastCheckError(fmt.Errorf("%w: library folder is missing", errCauseSetup))
		return
	}

	// The library is built with hardlinks. The config save endpoint probes this, but configs written
	// before this upgrade (or straight into config.json by docker/entrypoint.sh) never went through
	// it. Without this gate a filesystem without hardlink support happily downloads while every
	// JobOrganize dies, and the UI shows a healthy daemon. Probing here returns the same actionable
	// message as the endpoint, and aborts the pass: downloading what can't be organized only fills the
	// disk.
	// Direct library mode links nothing, so it only needs a library it can write to.
	if configs.IsDirectMode() {
		if err := files.ProbeWritable(configs.CompletedAnimePath); err != nil {
			logger.Logger.Error().Err(err).
				Str("completed_anime_path", configs.CompletedAnimePath).
				Msg("Completed anime path is not writable; skipping verification")
			state.SetLastCheckError(fmt.Errorf("%w: %w", errCauseLibraryUnwritable, err))
			return
		}
		// The incomplete folder's drive unmounted: every new download would stop at its first
		// file, and creating the folder would fill the system disk under the mount point. The
		// pass stops here, and once the folder is back the torrents waiting for it start again
		// (decisions/141).
		if staging := configs.StagingPath(); staging != "" {
			if err := files.ProbeIncompleteFolder(staging, configs.CompletedAnimePath); err != nil {
				logger.Logger.Error().Err(err).
					Str("incomplete_path", staging).
					Msg("Incomplete downloads folder is unavailable; skipping verification")
				state.SetLastCheckError(fmt.Errorf("%w: %w", errCauseIncomplete, err))
				return
			}
		}
	} else if librarian != nil {
		if err := librarian.ProbePath(configs.CompletedAnimePath); err != nil {
			logger.Logger.Error().Err(err).
				Str("completed_anime_path", configs.CompletedAnimePath).
				Msg("Completed anime path failed the hardlink probe; skipping verification")
			state.SetLastCheckError(fmt.Errorf("%w: %w", errCauseLibrary, err))
			return
		}
	}

	if backend == nil {
		logger.Logger.Error().Msg("Torrent backend not initialized; skipping verification")
		state.SetLastCheckError(fmt.Errorf("%w: not initialized", errCauseTorrent))
		return
	}

	// Safety net: main.go and PUT /config already apply the limit, but a hand-edited config.json never
	// goes through either. It's also what makes the queue a real reconciliation: every
	// SetMaxActiveDownloads runs an enforce, so a pause rain refused or a `stopping` left halfway
	// resolves itself on the next cycle. Runs BEFORE Ensure, when there may be no session yet — step 0
	// of enforce is what handles that (see decisions/041).
	backend.SetMaxActiveDownloads(configs.MaxConcurrentDownloads)
	// Same safety net for disable_seeding; Ensure below rebuilds the session when it changed.
	backend.SetUploadDisabled(configs.DisableSeeding)
	backend.SetDirectMode(configs.IsDirectMode())

	// Ensure the embedded torrent session exists for the current save path (created lazily,
	// recreated if the save path changed, if the download folder was swapped underneath, or if
	// disable_seeding changed).
	if _, err := backend.Ensure(configs.DownloadPath()); err != nil {
		logger.Logger.Error().Err(err).Msg("Failed to initialize embedded torrent session")
		state.SetLastCheckError(fmt.Errorf("%w: %w", errCauseTorrent, err))
		return
	}
	// Latched by Ensure, here or in an earlier manual-download call: the folder the session
	// was bound to is gone, so the records pointing into it must go too.
	if backend.ConsumeRootSwap() {
		cleared := clearLibraryPathsAfterRootSwap(fileManager, configs.CompletedAnimePath)
		// Even with 0 cleared: an empty library that vanished is still a folder that vanished, and an
		// unmounted drive is worth knowing about before the redownloads fill the disk underneath it.
		notifications.Notify(configs, notifications.LibraryMissing, libraryMissingItem(configs.CompletedAnimePath, cleared, configs.IsDirectMode()))
	}
	// Outside the direct-mode branch: placements made in direct mode keep staging after a switch
	// back. After Ensure: following a moved folder adds the torrent back to the session.
	adoptMovedStaging(backend, configs.StagingPath())
	followMovedRoots(configs, backend)
	resumeTorrentsWaitingForStaging(backend)

	// downloadedTorrents is an in-memory snapshot of the embedded client (cheap, no I/O).
	//
	// Taken BEFORE episodes.json is loaded, and that order matters: releaseTorrent (disable_seeding)
	// flags the record and then removes the torrent. A torrent already gone from this snapshot has
	// its flag on disk, so the later load sees it; the reverse order could read an unflagged record
	// next to a session without its torrent, and the pass would download the episode again.
	downloadedTorrents := backend.List()

	// A torrent at zero peers for hours doesn't come back on its own and holds a
	// max_concurrent_downloads slot indefinitely. Runs before the rest of the pass, but the snapshot
	// above is NOT retaken: whatever was dropped now is searched again on the next pass.
	dropStalledTorrents(downloadedTorrents, backend, fileManager, time.Now())

	// Same abort logic: with the AnimeIDs still in the old format (entry id) nothing on disk matches
	// AniList, and a pass in that state would re-download the whole library.
	if err := MigrateAnimeIDsToMedia(fileManager); err != nil {
		logger.Logger.Error().Err(err).Msg("Failed to migrate anime IDs to AniList media IDs; skipping verification")
		state.SetLastCheckError(fmt.Errorf("%w: %w", errCauseStorage, err))
		return
	}

	// Phase 1: fetch all independent data sources in parallel.
	var (
		anilistResponse *anilist.AniListResponse
		savedEpisodes   []files.EpisodeStruct
		blockedEpisodes []files.EpisodeKey
		// listStatuses[account][mediaId] — each account's list status of the animes on disk; see
		// fetchListStatuses.
		listStatuses map[string]map[int]anilist.MediaListStatus
		// onDisk is onDiskMediaIDs(savedEpisodes) without the blacklisted, computed once for the fetch
		// and the vote.
		onDisk []int

		errAnilist  error
		errEpisodes error
	)

	// Local, cheap read, done before the fan-out because searchAnilist depends on it. A failure here
	// doesn't abort the pass: without the file the daemon just stops covering the standalone animes.
	standaloneIDs, err := fileManager.LoadStandaloneAnimes()
	if err != nil {
		logger.Logger.Warn().Err(err).Msg("Failed to load standalone animes, continuing with the AniList lists only")
		standaloneIDs = nil
	}

	// The app's blacklist: this pass doesn't look those anime up, search, download or delete them
	// (decisions/155).
	blacklisted := blacklistedAnimes(fileManager)
	standaloneIDs = withoutBlacklistedIDs(standaloneIDs, blacklisted)

	var fetchWg sync.WaitGroup

	fetchWg.Add(1)
	go func() {
		defer fetchWg.Done()
		anilistResponse, errAnilist = searchAnilist(fileManager, configs, standaloneIDs)
	}()

	fetchWg.Add(1)
	go func() {
		defer fetchWg.Done()
		var e error
		savedEpisodes, e = fileManager.LoadSavedEpisodes()
		if e != nil {
			logger.Logger.Error().Err(e).Stack().Msg("Failed to load saved episodes")
			errEpisodes = e
			return
		}
		// Chained here, not in its own goroutine: the question is asked only about the animes on disk,
		// so it needs the file first. Still runs alongside searchAnilist.
		// Without the blacklisted: their list status is never asked, so no status rule deletes them.
		onDisk = withoutBlacklistedIDs(onDiskMediaIDs(savedEpisodes), blacklisted)
		listStatuses = fetchListStatuses(configs, onDisk)
	}()

	fetchWg.Add(1)
	go func() {
		defer fetchWg.Done()
		var e error
		blockedEpisodes, e = fileManager.LoadBlockedEpisodes()
		if e != nil {
			logger.Logger.Warn().Err(e).Msg("Failed to load blocked episodes, continuing without block list")
			blockedEpisodes = []files.EpisodeKey{}
		}
	}()

	fetchWg.Wait()

	if errAnilist != nil {
		state.SetLastCheckError(fmt.Errorf("%w: %w", errCauseAnilist, errAnilist))
		return
	}
	if errEpisodes != nil {
		state.SetLastCheckError(fmt.Errorf("%w: %w", errCauseStorage, errEpisodes))
		return
	}

	// A blacklisted anime's torrent that runs again (one the app had stopped, or one a pass added
	// right after the click) is paused here (decisions/155).
	keepBlacklistedPaused(fileManager, backend, savedEpisodes, blacklisted)

	// Reconciliation (durable safety net): enqueue JobOrganize for any completed torrent
	// whose episodes are not yet in the library. Covers completions missed while the daemon
	// was down and a save-path change. JobOrganize is idempotent, so re-runs are no-ops.
	reconcileLibrary(downloadedTorrents, savedEpisodes, jobQueue, configs.DisableSeeding)

	blockedMap := make(map[files.EpisodeKey]bool, len(blockedEpisodes))
	for _, k := range blockedEpisodes {
		blockedMap[k] = true
	}

	animes := withoutBlacklistedAnimes(anilistResponse.Data.Page.MediaList, blacklisted)

	// What the user already has in Shoko, by anime: never searched nor downloaded (the Shoko spec).
	animeSettings, _ := fileManager.LoadAllAnimeSettings()
	inShoko := state.Shoko().ForPass(ctx, configs, animes, animeSettings, time.Now())
	// A torrent handed to Shoko whose files Shoko moved can't seed them: it leaves the client, and
	// its records stay (decisions/152).
	releaseMovedByShoko(backend, fileManager, savedEpisodes, inShoko)
	// The anime set to remove the episodes in Shoko a pack brings: their files are skipped, or
	// deleted once downloaded (decisions/153).
	dropShokoPackFiles(configs, backend, fileManager, state.Shoko(), savedEpisodes, inShoko, animeSettings)

	// Absolute series axis of every anime in the pass AND of every anime_id with an episode on disk:
	// it's what lets the pass recognize that a pack downloaded under another cour already covers the
	// pending episode (coverage.go). A partial failure doesn't abort the pass — without the offset the
	// anime just doesn't adopt anything.
	animeIDs := make([]int, 0, len(animes))
	for _, a := range animes {
		animeIDs = append(animeIDs, a.Media.Id)
	}
	seriesIndex := resolveSeriesIndex(animeIDs, savedEpisodes)

	// Status deletion rule: EVERY account that has the anime must have it in some delete status (not
	// necessarily the same one). The download rule is the opposite — ONE account is enough —, and it's
	// already applied in searchAnilist through the union of the lists.
	customListed, customListsKnown := customListedMedia(configs)
	deletableMedia := withoutCustomListed(deletableMediaIDs(configs, listStatuses, onDisk), customListed, customListsKnown)

	var keysToDelete []files.EpisodeKey

	// One disk budget for the whole pass, shared by the parallel animes: what the queue still has to
	// write counts against the free space, and so does every torrent this pass adds
	// (decisions/101). From the same snapshot, so a torrent dropStalledTorrents just removed
	// still reserves its bytes until the next pass — the safe side.
	// Per download root, with each anime's root override (decisions/154).
	rootSettings, _ := fileManager.LoadAllAnimeSettings()
	disk := newDiskBudget(downloadedTorrents, savedEpisodes).withRoots(configs, backend, rootSettings)

	// Phase 2: process each anime concurrently, bounded by maxConcurrentAnimes, one download queue
	// after the other: a later queue's searches wait for the earlier queues' (decisions/164). Each
	// anime ranks with its queue's own priorities.
	sem := make(chan struct{}, maxConcurrentAnimes)
	resultCh := make(chan animeProcessResult, len(animes))

	var animeWg sync.WaitGroup
	start := time.Now()
	queues := loadQueues()

outer:
	for _, group := range queueGroups(animes, queues) {
		for _, anime := range group {
			select {
			case <-ctx.Done():
				break outer
			default:
			}

			// anime.Status belongs to ONE arbitrary account (the one that won the dedupe), so it can't
			// decide anything — the AND rule above is what answers.
			if deletableMedia[anime.Media.Id] {
				continue
			}

			_, queue := files.QueueOf(queues, anime.Media.Id)
			animeWg.Add(1)
			go func(a anilist.MediaList, ranking *nyaa.Priorities) {
				defer animeWg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				select {
				case <-ctx.Done():
					return
				default:
				}

				searcher := defaultNyaaSearcher()
				searcher.ranking = ranking
				resultCh <- processAnimeEpisodes(configs, backend, a, downloadedTorrents, savedEpisodes, seriesIndex, blockedMap, searcher, disk, shokoOwned{episodes: inShoko[a.Media.Id], trackInPacks: configs.KeepsShokoPackEpisodes(animeSettings[a.Media.Id])})
			}(anime, queue.Priorities)
		}
		// The next queue starts searching once this one is done.
		animeWg.Wait()
	}

	animeWg.Wait()
	close(resultCh)
	elapsed := time.Since(start)

	var newEpisodes []files.EpisodeStruct
	var checkedEpisodes []files.EpisodeKey
	var issues []Issue
	for r := range resultCh {
		newEpisodes = append(newEpisodes, r.newEpisodes...)
		checkedEpisodes = append(checkedEpisodes, r.checkedEpisodes...)
		keysToDelete = append(keysToDelete, r.keysToDelete...)
		issues = append(issues, r.issues...)
	}

	select {
	case <-ctx.Done():
		logger.Logger.Info().Msg("Verification cancelled")
		state.SetLastCheckError(nil)
		return
	default:
	}

	// Phase 3: sequential cleanup (file writes must not overlap).
	watching := make(map[int]bool, len(animes))
	for _, a := range animes {
		watching[a.Media.Id] = true
	}

	deleteEpisodesByStatus(configs, deletableMedia, fileManager, backend, librarian, savedEpisodes, newEpisodes, watching)

	handleSavedEpisodes(fileManager, configs, backend, librarian, handleEpisodesData{
		savedEpisodes:   savedEpisodes,
		keysToDelete:    keysToDelete,
		checkedEpisodes: checkedEpisodes,
		newEpisodes:     newEpisodes,
		watching:        watching,
		blacklisted:     blacklisted,
		customListed:    customListed,
		customListsGone: !customListsKnown,
	})

	state.SetLastCheck(time.Now())
	state.SetLastCheckError(nil)

	// AFTER SetLastCheckError, never before: it clears the report (see state.go). The cancellation
	// above also calls SetLastCheckError(nil) and returns, so an interrupted pass leaves no report —
	// which is right, it was incomplete.
	problems, limits := aggregateIssues(issues)
	state.SetLastCheckReport(CheckReport{
		FinishedAt: time.Now(),
		Problems:   problems,
		Limits:     limits,
	})

	// The alerts about the pass as a whole, from what it just found (daemon/health.go).
	// episodes.json again, not the snapshot from the start of the pass: the library alerts must not
	// report what the pass's own cleanup just deleted.
	afterPass, err := fileManager.LoadSavedEpisodes()
	if err != nil {
		logger.Logger.Warn().Err(err).Msg("Failed to reload saved episodes; the library alerts use the start-of-pass snapshot")
		afterPass = savedEpisodes
	} else {
		// Only with the reloaded list: the snapshot misses the records this pass just wrote.
		pruneOrphanPlacements(backend, afterPass, time.Now())
		pruneStagingLeftovers(backend, configs.StagingPath(), time.Now())
	}
	alertsAfterPass(configs, passAlerts{problems: problems, limits: limits, watching: watching, animes: animes, saved: afterPass,
		listsComplete: passLists.allRecordedSince(listDownload, configuredAccounts(configs), passStarted),
		now:           time.Now(),
	})

	if err := fileManager.DeleteEmptyFolders(configs.CompletedAnimePath); err != nil {
		logger.Logger.Warn().Err(err).Msg("Failed to delete empty folders")
	}

	avgTime := time.Duration(0)
	if len(checkedEpisodes) > 0 {
		avgTime = elapsed / time.Duration(len(checkedEpisodes))
	}

	logger.Logger.Info().
		Int("animes_checked", len(animes)).
		Int("episodes_checked", len(checkedEpisodes)).
		Int("episodes_downloaded", len(newEpisodes)).
		Dur("total_time", elapsed).
		Dur("avg_time_per_episode", avgTime).
		Msg("Verification completed")
}

// clearLibraryPathsAfterRootSwap wipes the LibraryPaths of every episode after the download
// root was swapped, so reconcileLibrary enqueues them again and the library is rebuilt at the
// configured path once the redownloads finish.
//
// This is the documented exception to decisions/029 (never clear LibraryPaths because a file
// is missing from disk). The rule exists so a per-file deletion by the user is not undone on
// the next pass, forever. A root swap is a different event: the whole folder the records
// point into is gone, the daemon is already redownloading its contents, and the detection is
// edge-triggered — Ensure reports the swap once, then rewrites the marker, so this runs a
// single time per swap and can never become the resurrection loop #29 guards against.
//
// Returns how many records it cleared — the episodes that will be downloaded again.
func clearLibraryPathsAfterRootSwap(fileManager FileManagerInterface, completedPath string) int {
	saved, err := fileManager.LoadSavedEpisodes()
	if err != nil {
		logger.Logger.Warn().Err(err).Msg("Root swap: failed to load saved episodes; library records left untouched")
		return 0
	}

	var stale []files.EpisodeStruct
	for _, ep := range saved {
		if len(ep.LibraryPaths) == 0 && !ep.TorrentReleased {
			continue
		}
		ep.LibraryPaths = nil
		// A released episode lived only in the library that is gone; without clearing the flag the
		// pass would keep treating it as present and never fetch it again.
		ep.TorrentReleased = false
		ep.HandedToShoko = false
		stale = append(stale, ep)
	}
	if len(stale) == 0 {
		return 0
	}

	if err := fileManager.UpsertEpisodes(stale); err != nil {
		logger.Logger.Warn().Err(err).Msg("Root swap: failed to clear the stale library paths")
		return 0
	}
	logger.Logger.Warn().
		Int("episodes", len(stale)).
		Str("completed_anime_path", completedPath).
		Msg("Root swap: the library folder is gone, cleared the stale library links; hardlink-mode episodes will be redownloaded at the configured path, direct-mode torrents stop with their files missing")
	return len(stale)
}

// libraryMissingItem words the library_missing alert. In direct mode the files lived in the library
// itself, and their torrents stop instead of downloading again on their own (decisions/139) until
// the user picks "Redownload missing files" (decisions/140); torrents added in hardlink mode still
// download again.
func libraryMissingItem(completedPath string, cleared int, direct bool) notifications.Episode {
	if direct {
		return notifications.Episode{Text: fmt.Sprintf("%s is gone or was replaced. Torrents written straight to it stop and show their files missing; use \"Redownload missing files\" in Downloads to get them back.", completedPath)}
	}
	return notifications.Episode{Text: fmt.Sprintf("%s is gone or was replaced. %s will be downloaded again.", completedPath, plural(cleared, "episode"))}
}

// reconcileLibrary enqueues JobOrganize for completed torrents whose saved episodes have
// not yet been hardlinked into the library (empty LibraryPaths). Enqueue is deduped, so
// repeated passes are cheap.
//
// With disableSeeding, EVERY completed torrent with records goes back: organize releases the
// already-organized ones (releaseTorrent). That is how turning the setting on stops the torrents
// that were seeding before it, and how a release interrupted by a crash finishes.
func reconcileLibrary(downloaded []torrents.TorrentInfo, savedEpisodes []files.EpisodeStruct, jobQueue *JobQueue, disableSeeding bool) {
	if jobQueue == nil {
		return
	}
	byHash := make(map[string][]files.EpisodeStruct)
	for _, ep := range savedEpisodes {
		if ep.EpisodeHash != "" {
			byHash[ep.EpisodeHash] = append(byHash[ep.EpisodeHash], ep)
		}
	}
	for _, t := range downloaded {
		if !t.Completed {
			continue
		}
		eps := byHash[t.Hash]
		if len(eps) == 0 {
			continue // orphan torrent with no episode record; nothing to organize
		}
		needs := disableSeeding
		for _, ep := range eps {
			if len(ep.LibraryPaths) == 0 {
				needs = true
				break
			}
		}
		if needs {
			jobQueue.EnqueueOrganize(t.Hash)
		}
	}
}

// onDiskMediaIDs is every anime with an episode on disk that status deletion may touch — the
// manually managed ones never are. Sorted, so the requests built from it are stable.
func onDiskMediaIDs(savedEpisodes []files.EpisodeStruct) []int {
	seen := make(map[int]bool)
	var ids []int
	for _, ep := range savedEpisodes {
		if ep.AnimeID != 0 && !ep.ManuallyManaged && !seen[ep.AnimeID] {
			seen[ep.AnimeID] = true
			ids = append(ids, ep.AnimeID)
		}
	}
	slices.Sort(ids)
	return ids
}

// fetchListStatuses asks every account for its list status of the animes ON DISK, and of nothing
// else: those are the only ones status deletion can act on. Result: account -> media id -> status.
// An id the account doesn't track is absent from its map; an account whose fetch failed is absent
// from the result, and absent never agrees (deletableMediaIDs). MAL accounts are keyed by
// malAccountKey.
//
// It replaced downloading every account's delete-status lists whole — 23 pages per pass for an
// account with 1124 COMPLETED/DROPPED/PLANNING entries, plus one tie-break query per account that
// didn't list a candidate — which alone went past AniList's 30 req/min on every pass. One
// `mediaId_in` request per account per 50 animes on disk answers both questions at once
// (decisions/130).
//
// Each account's ids in a delete status also go into passLists as its listDelete half: the
// webhook source filter only asks about animes with files, which is this set — plus the ones this
// pass is about to delete, which recordCarrying keeps for one more pass.
func fetchListStatuses(configs *files.Config, onDisk []int) map[string]map[int]anilist.MediaListStatus {
	if len(configs.DeleteStatuses) == 0 {
		return nil
	}
	onDiskSet := make(map[int]bool, len(onDisk))
	for _, id := range onDisk {
		onDiskSet[id] = true
	}
	result := make(map[string]map[int]anilist.MediaListStatus, len(configs.AnilistUsernames)+len(configs.MALUsernames))

	// key is the account's key in the result (malAccountKey for MAL); passLists takes source + user.
	record := func(key, source, user string, statuses map[int]anilist.MediaListStatus) {
		result[key] = statuses
		inDelete := make(map[int]bool)
		for id, status := range statuses {
			if isInDeleteStatuses(configs.DeleteStatuses, status) {
				inDelete[id] = true
			}
		}
		passLists.recordCarrying(listDelete, notifications.AccountSource(source, user), inDelete)
	}

	for _, username := range configs.AnilistUsernames {
		statuses, err := anilist.GetListStatusesByMediaIDs(username, onDisk, anilist.PriorityCritical)
		// ponytail: the local cache's answer votes on deletion as if it were fresh, by explicit decision
		// of the project owner. The risk has a name: an anime that left a delete status AFTER the
		// snapshot (COMPLETED that became REPEATING, and the pass downloaded the season) shows up as
		// deletable again, and the pass deletes what it just downloaded. If this ever bites, the fix is
		// one line: treat ErrFromCache as an account with no answer here, and deletableMediaIDs'
		// unanimity refuses on its own.
		if err != nil && !errors.Is(err, anilist.ErrFromCache) {
			logger.Logger.Warn().Err(err).Str("username", username).Int("media_ids", len(onDisk)).
				Msg("Failed to fetch the AniList list statuses of the animes on disk")
			continue
		}
		record(username, anilist.AccountSourceAniList, username, statuses)
	}

	for _, user := range configs.MALUsernames {
		entries, err := anilist.GetMALEntries(user, anilist.PriorityCritical)
		// ErrFromCache votes as if fresh, the same explicit choice as the AniList loop above.
		// ErrMALEmpty is a failure, not an empty answer: an account that "tracks nothing" would lose
		// its veto — see anilist.ErrMALEmpty.
		if err != nil && !errors.Is(err, anilist.ErrFromCache) {
			logger.Logger.Warn().Err(err).Str("mal_user", user).Msg("Failed to fetch MAL list for delete statuses")
			continue
		}
		statuses := make(map[int]anilist.MediaListStatus)
		for _, entry := range entries {
			if onDiskSet[entry.MediaID] {
				statuses[entry.MediaID] = entry.Status
			}
		}
		record(malAccountKey(user), anilist.AccountSourceMAL, user, statuses)
	}
	return result
}

// deletableMediaIDs decides which animes may have their episodes deleted by status.
//
// Rule: EVERY account (AniList or MyAnimeList) that has the anime in its list must have it in some
// delete status — the statuses don't have to be the same (DROPPED in one and COMPLETED in another
// deletes). An account that doesn't track the anime doesn't vote; one that has it in a neutral
// status (PLANNING, for example) vetoes, otherwise we'd delete episodes another account still means
// to watch. An account whose fetch failed (absent from listStatuses) vetoes too. And an anime no
// account holds in a delete status isn't a candidate at all.
//
// No network: fetchListStatuses already brought every account's status of every anime on disk.
//
// onDisk is onDiskMediaIDs of the pass's saved episodes, the same set fetchListStatuses asked about.
func deletableMediaIDs(configs *files.Config, listStatuses map[string]map[int]anilist.MediaListStatus, onDisk []int) map[int]bool {
	if len(configs.DeleteStatuses) == 0 || len(listStatuses) == 0 {
		return nil
	}

	accounts := make([]string, 0, len(configs.AnilistUsernames)+len(configs.MALUsernames))
	accounts = append(accounts, configs.AnilistUsernames...)
	for _, user := range configs.MALUsernames {
		accounts = append(accounts, malAccountKey(user))
	}

	deletable := make(map[int]bool)
	for _, mediaID := range onDisk {
		if allAccountsAgreeOnDelete(configs.DeleteStatuses, accounts, listStatuses, mediaID) {
			deletable[mediaID] = true
		}
	}
	return deletable
}

func allAccountsAgreeOnDelete(deleteStatuses, accounts []string, listStatuses map[string]map[int]anilist.MediaListStatus, mediaID int) bool {
	wanted := false
	for _, account := range accounts {
		statuses, fetched := listStatuses[account]
		if !fetched {
			// This account's fetch failed: without its opinion there's no unanimity.
			logAccount(logger.Logger.Debug(), account).Int("media_id", mediaID).
				Msg("Skipping status deletion: account list unavailable")
			return false
		}
		status, tracked := statuses[mediaID]
		switch {
		case !tracked:
			continue // the account doesn't track this anime, no vote
		case isInDeleteStatuses(deleteStatuses, status):
			wanted = true
		default:
			logAccount(logger.Logger.Debug(), account).Int("media_id", mediaID).
				Str("status", string(status)).Msg("Status deletion vetoed by account")
			return false
		}
	}
	return wanted
}

// malAccountKey keys a MAL account in listStatuses. The prefix keeps an AniList and a MAL
// account with the same name from overwriting each other.
func malAccountKey(user string) string { return "mal:" + user }

// logAccount names a listStatuses account in a log line the way the rest of the daemon does: an
// AniList account under "username", a MAL one under "mal_user" without the key's prefix.
func logAccount(e *zerolog.Event, account string) *zerolog.Event {
	if user, ok := strings.CutPrefix(account, "mal:"); ok {
		return e.Str("mal_user", user)
	}
	return e.Str("username", account)
}

// customListExtras returns an AniList account's entries that are in one of its download custom
// lists and in a list status the download statuses leave out (decisions/162): membership from
// GetCustomListMembers, the media from GetMediaByIDs (batched, cached per id), the media-status
// filter applied. members is every media id in those lists, before the media-status cut, for the
// webhook source filter. An excluded list still wins, per episode (animeIsInExcludedList).
func customListExtras(username string, configs *files.Config) (extras []anilist.MediaList, members []int) {
	lists := configs.CustomListsOf(username)
	if len(lists) == 0 {
		return nil, nil
	}
	got, err := anilist.GetCustomListMembers(username, lists, anilist.PriorityCritical)
	if err != nil && !errors.Is(err, anilist.ErrFromCache) {
		logger.Logger.Warn().Err(err).Str("username", username).Msg("Failed to read the download custom lists; the rest of the pass goes on")
		return nil, nil
	}
	byMedia := map[int]anilist.CustomListMember{}
	var ids []int
	for _, m := range got {
		members = append(members, m.MediaID)
		// In a download status, the main fetch has it already.
		if _, dup := byMedia[m.MediaID]; dup || slices.Contains(configs.DownloadStatuses, string(m.Status)) {
			continue
		}
		byMedia[m.MediaID] = m
		ids = append(ids, m.MediaID)
	}
	if len(ids) == 0 {
		return nil, members
	}
	media, err := anilist.GetMediaByIDs(ids, anilist.PriorityCritical)
	if err != nil {
		logger.Logger.Warn().Err(err).Str("username", username).Msg("Failed to fetch some anime of the download custom lists")
	}
	for _, id := range ids {
		found := media[id]
		if found == nil {
			continue // unknown to AniList, or not fetched this time
		}
		ml := *found
		m := byMedia[id]
		ml.Id, ml.Status, ml.Progress, ml.CustomLists = m.EntryID, m.Status, m.Progress, m.AsCustomLists()
		// A Completed entry is in the list to be downloaded whole: its progress (the total) would read
		// every episode as watched. Its 0 wins the dedupe on purpose (decisions/162).
		if m.Status == anilist.MediaListStatusCompleted {
			ml.Progress = 0
		}
		if anilist.MediaStatusAllowed(configs.DownloadMediaStatuses, ml.Media.Status) {
			extras = append(extras, ml)
		}
	}
	return extras, members
}

// customListedMedia is every media id in a download custom list of any account, and whether every
// such account's lists could be read (decisions/162). It reads what searchAnilist read: the cached
// membership, or the failure GetCustomListMembers remembers for a minute.
func customListedMedia(configs *files.Config) (listed map[int]bool, known bool) {
	listed = map[int]bool{}
	for _, user := range configs.AnilistUsernames {
		lists := configs.CustomListsOf(user)
		if len(lists) == 0 {
			continue
		}
		got, err := anilist.GetCustomListMembers(user, lists, anilist.PriorityCritical)
		if err != nil && !errors.Is(err, anilist.ErrFromCache) {
			logger.Logger.Debug().Str("username", user).Msg("Download custom lists unavailable: automatic deletion waits for the next pass")
			return nil, false
		}
		for _, m := range got {
			listed[m.MediaID] = true
		}
	}
	return listed, true
}

// completedCustomListMember reports whether the pass counts an anime's progress as 0: a Completed
// entry of a download custom list, outside the download statuses (decisions/162). resolveAnimeDetails
// asks, so the debug run selects what the pass downloads (decisions/058).
func completedCustomListMember(configs *files.Config, mediaID int) bool {
	if slices.Contains(configs.DownloadStatuses, string(anilist.MediaListStatusCompleted)) {
		return false
	}
	for _, user := range configs.AnilistUsernames {
		lists := configs.CustomListsOf(user)
		if len(lists) == 0 {
			continue
		}
		got, err := anilist.GetCustomListMembers(user, lists, anilist.PriorityCritical)
		if err != nil && !errors.Is(err, anilist.ErrFromCache) {
			continue
		}
		for _, m := range got {
			if m.MediaID == mediaID && m.Status == anilist.MediaListStatusCompleted {
				return true
			}
		}
	}
	return false
}

// withoutCustomListed takes the anime in a download custom list out of status deletion: being in
// the list is the user's choice to have it, so it vetoes like an account holding it in a neutral
// status (decisions/043, decisions/162). With an account's lists unreadable, status deletion
// waits for the next pass, as with an account whose list failed.
func withoutCustomListed(deletable map[int]bool, listed map[int]bool, known bool) map[int]bool {
	if !known {
		return nil
	}
	for id := range listed {
		delete(deletable, id)
	}
	return deletable
}

// searchAnilist builds the pass's anime universe: the union of the configured accounts' lists
// (AniList and MyAnimeList) plus the standalone animes (§ standalone).
//
// fileManager comes in here, not just the config, because of the automatic removal: a standalone
// anime that later showed up in a list leaves the file, and the only moment that's known is exactly
// here, with the merged list in hand.
func searchAnilist(fileManager FileManagerInterface, configs *files.Config, standaloneIDs []int) (*anilist.AniListResponse, error) {
	// No list account is NOT an error: the pass still has work to do (the standalone animes), and
	// aborting here would make the feature never run on an install without a list. No library is:
	// there's nowhere to download to.
	if configs.DownloadPath() == "" {
		err := fmt.Errorf("missing required configuration: completed anime path")
		logger.Logger.Error().
			Err(err).
			Str("download_path", configs.DownloadPath()).
			Msg("Missing required configuration: completed anime path")
		return nil, err
	}

	merged := &anilist.AniListResponse{}
	var lastErr error
	for _, username := range configs.AnilistUsernames {
		// Fetch customLists first via a minimal query (before the complex query that may null it out).
		clMap := anilist.GetCustomListsMap(username, configs.DownloadStatuses, anilist.PriorityCritical)

		resp, err := anilist.GetAllCurrentAnime(username, configs.DownloadStatuses)
		// The local cache serves the pass: episodes already aired stay downloadable and Nyaa didn't go
		// down with it. TimeUntilAiring comes rebased (see anilist/persist.go), so the episode that was
		// ten minutes away when AniList went down enters the search on its own.
		if err != nil && !errors.Is(err, anilist.ErrFromCache) {
			logger.Logger.Error().Err(err).Stack().
				Str("username", username).
				Msg("Failed to search animes on Anilist")
			lastErr = err
			continue
		}

		// The account's download custom lists, whatever the entries' list status (decisions/162).
		extras, members := customListExtras(username, configs)

		// Before the media-status cut: the account still has the anime in a download status or a
		// download custom list, and that is what the webhook source filter asks (decisions/126).
		owned := make(map[int]bool, len(resp.Data.Page.MediaList)+len(members))
		for _, ml := range resp.Data.Page.MediaList {
			owned[ml.Media.Id] = true
		}
		for _, id := range members {
			owned[id] = true
		}
		passLists.record(listDownload, notifications.AccountSource(anilist.AccountSourceAniList, username), owned)

		var filtered []anilist.MediaList
		for i := range resp.Data.Page.MediaList {
			ml := &resp.Data.Page.MediaList[i]
			if !anilist.MediaStatusAllowed(configs.DownloadMediaStatuses, ml.Media.Status) {
				logger.Logger.Debug().
					Int("anime_id", ml.Id).
					Str("media_status", string(ml.Media.Status)).
					Msg("Skipping anime: media status not in DownloadMediaStatuses")
				continue
			}
			if cl, ok := clMap[ml.Id]; ok && len(cl) > 0 {
				ml.CustomLists = cl
			}
			filtered = append(filtered, *ml)
		}
		resp.Data.Page.MediaList = append(filtered, extras...)

		count := len(resp.Data.Page.MediaList)
		logger.Logger.Debug().
			Str("username", username).
			Int("animes_found", count).
			Msg("Fetched animes from Anilist for username")
		merged.Data.Page.MediaList = append(merged.Data.Page.MediaList, resp.Data.Page.MediaList...)
	}

	var lastMALErr error
	for _, user := range configs.MALUsernames {
		list, err := anilist.GetMALList(user, configs.DownloadStatuses, anilist.PriorityCritical)
		if err != nil && !errors.Is(err, anilist.ErrFromCache) {
			logger.Logger.Error().Err(err).Str("mal_user", user).Msg("Failed to search animes on MyAnimeList")
			lastMALErr = err
			continue
		}
		owned := make(map[int]bool, len(list))
		for _, ml := range list {
			owned[ml.Media.Id] = true
		}
		passLists.record(listDownload, notifications.AccountSource(anilist.AccountSourceMAL, user), owned)
		count := 0
		for _, ml := range list {
			if anilist.MediaStatusAllowed(configs.DownloadMediaStatuses, ml.Media.Status) {
				merged.Data.Page.MediaList = append(merged.Data.Page.MediaList, ml)
				count++
			}
		}
		logger.Logger.Debug().Str("mal_user", user).Int("animes_found", count).Msg("Fetched animes from MyAnimeList for username")
	}

	if len(merged.Data.Page.MediaList) == 0 {
		// The banner names the source that failed. With both failing it stays AniList.
		if lastErr != nil {
			return nil, fmt.Errorf("failed to search animes on Anilist: %w", lastErr)
		}
		if lastMALErr != nil {
			return nil, fmt.Errorf("%w: failed to search animes on MyAnimeList: %w", errCauseMAL, lastMALErr)
		}
	}

	merged.Data.Page.MediaList = anilist.DedupeByMedia(merged.Data.Page.MediaList)

	// AFTER the dedupe, never before: the real entry has to win. DedupeByMedia keeps the LOWEST
	// progress, so the Progress 0 of GetMediaByID's synthetic MediaList would win and the daemon would
	// go back to downloading already-watched episodes.
	merged.Data.Page.MediaList = appendStandaloneAnimes(fileManager, merged.Data.Page.MediaList, standaloneIDs)

	logger.Logger.Info().
		Strs("usernames", configs.AnilistUsernames).
		Strs("mal_usernames", configs.MALUsernames).
		Int("animes_found", len(merged.Data.Page.MediaList)).
		Msg("Successfully fetched animes from Anilist")

	return merged, nil
}
