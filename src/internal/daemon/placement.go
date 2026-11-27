package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/nyaa"
	"AutoAnimeDownloader/src/internal/torrents"
)

// torrentTarget is what the daemon knows about a torrent it is about to add: enough for direct
// library mode to decide where the files go before rain asks for them.
type torrentTarget struct {
	anime anilist.MediaList
	// series is the anime's PREQUEL chain; nil when the index didn't resolve it (AniList down
	// on a first run). The folder then falls back to the anime's own title.
	series *anilist.Series
	// episode is the local episode number for a single-episode torrent; 0 for a pack.
	episode int
	batch   bool
	// root is the anime's download root override (AnimeSettings.Root) for a manual download; the
	// pass reads it from the disk budget instead (decisions/154).
	root string
}

// placedOrder is order for a torrent whose hash already has a placement (a retry of a failed one, a
// re-add of a Kept one): register keeps the first placement (decisions/136), so the torrent goes
// where it went, and the budget must count it on that root, not on the one the order would pick.
func placedOrder(configs *files.Config, backend torrents.TorrentBackend, magnet string, order []files.DownloadRoot) []files.DownloadRoot {
	if backend == nil {
		return order
	}
	hash, err := torrents.InfoHash(magnet)
	if err != nil {
		return order
	}
	p, ok := backend.Placement(hash)
	if !ok {
		return order
	}
	root := files.DownloadRoot{Name: p.RootName, Path: p.RootOr(configs.CompletedAnimePath)}
	if root.Name == "" {
		root.Name = files.LibraryRootName
	}
	// Its root is gone (an unmounted drive): nowhere to put it, rather than create the folder.
	if p.RootPath != "" && !dirExists(p.RootPath) {
		return []files.DownloadRoot{}
	}
	return []files.DownloadRoot{root}
}

// availableRoots is download_roots without the ones whose folder isn't there (an unmounted drive):
// a torrent placed there would create the folder on the disk its mount point sits on. The library
// is left to its own probe (library_missing). decisions/154.
func availableRoots(configs *files.Config) []files.DownloadRoot {
	var out []files.DownloadRoot
	for _, r := range configs.Roots() {
		if r.Path == configs.CompletedAnimePath || dirExists(r.Path) {
			out = append(out, r)
		}
	}
	return out
}

// rootOrder is where a torrent of target may go, in the order tried (decisions/154): the anime's
// override, the root that already holds its series (so a new season lands next to the others),
// then download_roots in the order listed. The disk budget takes the first one with space, each
// root kept to its own min_free_gb, so one drive fills before the next is used.
func rootOrder(configs *files.Config, backend torrents.TorrentBackend, target torrentTarget, override string) []files.DownloadRoot {
	roots := availableRoots(configs)
	out := make([]files.DownloadRoot, 0, len(roots))
	seen := map[string]bool{}
	add := func(r files.DownloadRoot) {
		if !seen[r.Path] {
			seen[r.Path] = true
			out = append(out, r)
		}
	}
	if override != "" {
		for _, r := range roots {
			if r.Name == override {
				add(r)
			}
		}
	}
	if id := seriesID(target); id != 0 && backend != nil && len(roots) > 1 {
		for _, p := range backend.Placements() {
			if p.SeriesID != id {
				continue
			}
			held := p.RootOr(configs.CompletedAnimePath)
			for _, r := range roots {
				if r.Path == held {
					add(r)
				}
			}
			break
		}
	}
	for _, r := range roots {
		add(r)
	}
	return out
}

// manualTarget is the target of a manual download. Only direct mode needs the series chain, so
// hardlink mode never makes the extra (cached, one-unit) AniList call.
func manualTarget(configs *files.Config, ml anilist.MediaList, episode int, batch bool) torrentTarget {
	target := torrentTarget{anime: ml, episode: episode, batch: batch}
	if configs.IsDirectMode() {
		target.series = lookupSeries(ml.Media.Id)
	}
	return target
}

// standaloneTarget is the target of a whole-title magnet for a standalone anime, which reaches
// the add before anything fetched the media. Hardlink mode needs none of it.
func standaloneTarget(configs *files.Config, mediaID int) (torrentTarget, error) {
	if !configs.IsDirectMode() {
		return torrentTarget{batch: true}, nil
	}
	ml, err := anilist.GetMediaByID(mediaID, anilist.PriorityCritical)
	if ml == nil {
		if err == nil {
			err = fmt.Errorf("anime %d does not exist on AniList", mediaID)
		}
		return torrentTarget{}, fmt.Errorf("cannot place the torrent without the anime's details: %w", err)
	}
	return manualTarget(configs, *ml, 0, true), nil
}

// lookupSeries resolves one anime's PREQUEL chain. Best effort: nil when AniList can't answer,
// and the series folder then falls back to the anime's own title.
func lookupSeries(mediaID int) *anilist.Series {
	index, err := anilist.GetSeriesIndex([]int{mediaID}, anilist.PriorityCritical)
	if series, ok := index[mediaID]; ok {
		return &series
	}
	if err != nil {
		logger.Logger.Debug().Err(err).Int("media_id", mediaID).Msg("Could not resolve the series chain; naming the folder after the anime")
	}
	return nil
}

// registerPlacement tells the backend where a torrent's files go, BEFORE the add: rain asks for
// the storage synchronously inside the add. A no-op in hardlink mode. A magnet whose hash can't
// be read is left to Add, which reports the same error.
func registerPlacement(configs *files.Config, backend torrents.TorrentBackend, magnet string, target torrentTarget, root files.DownloadRoot) error {
	if !configs.IsDirectMode() {
		return nil
	}
	hash, err := torrents.InfoHash(magnet)
	if err != nil {
		return nil
	}
	req := placementRequest(configs, hash, target)
	// The download root the disk budget chose (decisions/154). The library stays implicit: its
	// placements resolve against wherever completed_anime_path is, so a moved library keeps its
	// seeds (decisions/136).
	req.RootName = root.Name
	if root.Path != "" && root.Path != configs.CompletedAnimePath {
		req.RootPath = root.Path
	}
	pinSeriesDir(backend, &req)
	if err := backend.SetPlacement(req); err != nil {
		return fmt.Errorf("failed to place torrent %s in the library: %w", hash, err)
	}
	logger.Logger.Debug().Str("hash", hash).Str("series_dir", req.SeriesDir).Str("kind", req.Kind).
		Int("season", req.Season).Msg("Registered direct-mode placement")
	return nil
}

// pinSeriesDir keeps a series in the folder it already has. The folder is named after the chain
// root's title, and AniList edits titles: without this, a season added after an edit would land
// in a second, differently named folder next to the first. Any existing placement with the same
// series id wins (its SeriesDir is one path component, so Base works resolved or not).
func pinSeriesDir(backend torrents.TorrentBackend, req *torrents.PlacementRequest) {
	if req.SeriesID == 0 {
		return
	}
	for _, p := range backend.Placements() {
		if p.SeriesID == req.SeriesID && p.Hash != req.Hash && p.SeriesDir != "" {
			req.SeriesDir = filepath.Base(p.SeriesDir)
			return
		}
	}
}

// placementRequest decides a torrent's place. Pure: everything comes from the target and the
// config.
func placementRequest(configs *files.Config, hash string, target torrentTarget) torrents.PlacementRequest {
	seriesName := seriesTitle(target)
	req := torrents.PlacementRequest{
		Hash: hash,
		// Relative to the library root: the placement resolves it against wherever the library is
		// when a file is opened, so a moved library keeps its seeds (decisions/136).
		SeriesDir:  files.SanitizeName(seriesName),
		SeriesName: seriesName,
		SeriesID:   seriesID(target),
		PartFiles:  configs.UsePartFiles(),
		// Frozen into the placement: changing the setting later only affects new torrents
		// (decisions/141).
		IncompleteRoot: configs.StagingPath(),
	}
	switch {
	case target.anime.Media.Format == anilist.MediaFormatMovie:
		req.Kind = torrents.PlacementMovie
	case target.batch:
		req.Kind = torrents.PlacementPack
	default:
		req.Kind = torrents.PlacementEpisode
		season := seasonNumber(target)
		req.Season = season
		if configs.RenameFilesForJellyfin && target.episode > 0 {
			req.RenameVideo = episodeFileName(configs, seriesName, season, target)
		}
	}
	return req
}

// seriesTitle names the series folder: the title of the PREQUEL chain's root, so every season
// lands in one folder. Unknown chain (or a link cached before titles were fetched) → the anime's
// own title, which is what hardlink mode names its folder.
func seriesTitle(target torrentTarget) string {
	if target.series != nil {
		if title := target.series.RootTitle(); title != "" {
			return title
		}
	}
	return getAnimeTitleSafe(target.anime)
}

// seriesID is the AniList id the series folder's tvshow.nfo carries: the chain root when its
// title named the folder, else the anime itself (the folder is then named after it).
func seriesID(target torrentTarget) int {
	if target.series != nil && target.series.RootTitle() != "" {
		return target.series.Key
	}
	return target.anime.Media.Id
}

// seasonNumber is the anime's season (see chainSeasons). With no chain it is the marker in the
// anime's own titles, else 1.
func seasonNumber(target torrentTarget) int {
	if seasons := targetSeasons(target); len(seasons) > 0 {
		return seasons[len(seasons)-1]
	}
	if season, _ := seasonPartOf(target.anime.Media); season != nil && *season >= 1 {
		return *season
	}
	return 1
}

// targetSeasons is chainSeasons for the target's chain, with the anime itself read from its
// synonyms too (seasonPartOf, decisions/132). Nil when the chain is unknown.
func targetSeasons(target torrentTarget) []int {
	if target.series == nil || len(target.series.Chain) == 0 {
		return nil
	}
	season, part := seasonPartOf(target.anime.Media)
	return chainSeasons(target.series.Chain, season, part)
}

// chainSeasons numbers the seasons of a PREQUEL chain, root first. Each entry takes the season
// marker in its titles ("Season 2", "2nd Season", "II"). One without a marker continues its
// prequel's season when it is a later part ("Part 2", "Cour 2" — a part is not a new season,
// decisions/010), and starts the next season otherwise; the root starts season 1. The last entry
// is the anime itself, whose season and part come from the caller (they also read its synonyms).
//
// Mushoku Tensei: "…Honki Dasu" = 1, "…Honki Dasu Part 2" = 1, "Mushoku Tensei II…" = 2,
// "Mushoku Tensei II… Part 2" = 2.
func chainSeasons(chain []anilist.ChainEntry, selfSeason, selfPart *int) []int {
	seasons := make([]int, len(chain))
	for i, entry := range chain {
		season, part := selfSeason, selfPart
		if i < len(chain)-1 {
			season, part = titleSeasonPart(entry)
		}
		switch {
		case season != nil && *season >= 1:
			seasons[i] = *season
		case i == 0:
			seasons[i] = 1
		case part != nil && *part >= 2:
			seasons[i] = seasons[i-1]
		default:
			seasons[i] = seasons[i-1] + 1
		}
	}
	return seasons
}

// titleSeasonPart reads season and part from a chain entry's titles, English first, each one
// independently (the same order as ExtractAnimeSeasonPart).
func titleSeasonPart(entry anilist.ChainEntry) (season, part *int) {
	for _, title := range []string{entry.English, entry.Romaji} {
		if title == "" {
			continue
		}
		if season == nil {
			season = nyaa.ExtractSeason(title)
		}
		if part == nil {
			part = nyaa.ExtractPart(title)
		}
	}
	return season, part
}

// episodeFileName is the Jellyfin name of a single episode in direct mode, "Series - S02E13".
//
// A split cour (Part 1 and Part 2 are separate AniList entries, each numbered from 1) shares its
// season folder, so the two E01 would collide. episode_numbering picks the fix: "season" adds
// the episodes of the earlier entries of the same season (TVDB/TMDB numbering), "entry" keeps the
// entry's numbers and tags the part.
func episodeFileName(configs *files.Config, seriesName string, season int, target torrentTarget) string {
	number := target.episode
	tag := ""
	if configs.EpisodeNumbering == files.EpisodeNumberingEntry {
		if _, part := seasonPartOf(target.anime.Media); part != nil && *part > 1 {
			tag = fmt.Sprintf(" (Part %d)", *part)
		}
	} else if seasons := targetSeasons(target); len(seasons) > 0 {
		chain := target.series.Chain
		for i := 0; i < len(chain)-1; i++ {
			if seasons[i] == season {
				number += chain[i].Episodes
			}
		}
	}
	return fmt.Sprintf("%s - S%02dE%02d%s", files.SanitizeName(seriesName), season, number, tag)
}

// orphanPlacementGrace keeps a young placement out of the orphan sweep: SetPlacement runs just
// before the add, and a sweep landing in between would find no torrent and no record yet.
const orphanPlacementGrace = time.Hour

// pruneOrphanPlacements deletes the files of placements nothing uses any more: no torrent in the
// session and no episode record pointing at the hash. That is a failed torrent that kept its
// partial data for a resume (HandleTorrentFailure, decisions/024) and whose episode was then
// downloaded from ANOTHER release — in hardlink mode that leftover sat in .torrents; here it would
// sit in the library. A torrent removed on purpose with keep_data was marked Kept first, and a Kept
// placement is only forgotten here, its files never touched.
//
// saved must be episodes.json AFTER the pass: the start-of-pass snapshot misses this pass's own
// records and would sweep the torrents it just added.
func pruneOrphanPlacements(backend torrents.TorrentBackend, saved []files.EpisodeStruct, now time.Time) {
	referenced := make(map[string]bool, len(saved))
	for _, ep := range saved {
		if ep.EpisodeHash != "" {
			referenced[ep.EpisodeHash] = true
		}
	}
	for _, p := range backend.Placements() {
		_, inSession := backend.Get(p.Hash)
		// Removed by hand with keep_data: the files are the user's. A Kept placement is never swept
		// and never forgotten (decisions/137), so a later re-add of the hash, whenever it comes,
		// resumes into the files instead of downloading a [hash] copy next to them.
		if p.Kept {
			continue
		}
		// Unresolved paths (no session yet) would be relative to the process's working dir.
		if inSession || referenced[p.Hash] || now.Sub(p.CreatedAt) < orphanPlacementGrace || !filepath.IsAbs(p.SeriesDir) {
			continue
		}
		if err := torrents.RemovePlacedFiles(p); err != nil {
			logger.Logger.Warn().Err(err).Str("hash", p.Hash).Msg("Failed to delete an abandoned download's files")
			continue
		}
		if err := backend.ForgetPlacement(p.Hash); err != nil {
			logger.Logger.Warn().Err(err).Str("hash", p.Hash).Msg("Failed to forget an abandoned download's placement")
			continue
		}
		logger.Logger.Info().Str("hash", p.Hash).Str("series_dir", p.SeriesDir).Msg("Deleted the files of an abandoned direct-mode download")
	}
}

// resumeTorrentsWaitingForStaging starts again the FINISHED torrents that stopped because their
// incomplete folder was gone (decisions/141), once it is back. Unfinished ones need nothing: the
// download queue starts every stopped incomplete torrent on its own.
func resumeTorrentsWaitingForStaging(backend torrents.TorrentBackend) {
	for _, t := range backend.List() {
		if !t.Completed || t.Status != torrents.StatusStopped {
			continue
		}
		staging := errors.Is(t.StopError, torrents.ErrIncompleteFolderUnavailable)
		root := errors.Is(t.StopError, torrents.ErrDownloadRootUnavailable)
		if !staging && !root {
			continue
		}
		p, ok := backend.Placement(t.Hash)
		if !ok || (staging && (p.IncompleteRoot == "" || !dirExists(p.IncompleteRoot))) || (root && (p.RootPath == "" || !dirExists(p.RootPath))) {
			continue
		}
		if err := backend.Resume(t.Hash); err != nil {
			logger.Logger.Warn().Err(err).Str("hash", t.Hash).Msg("Failed to restart a torrent whose incomplete folder is back")
			continue
		}
		logger.Logger.Info().Str("hash", t.Hash).Msg("The incomplete downloads folder is back; restarted the torrent")
	}
}

// followMovedRoots re-points the placements of a download root the user moved: its folder is gone,
// and download_roots now names that root at another path (decisions/154). rain adds the torrent
// back so it re-checks the moved files, as for a moved incomplete folder (decisions/141). A root
// that is gone and not re-pointed is left alone: its drive may just be unmounted.
func followMovedRoots(configs *files.Config, backend torrents.TorrentBackend) {
	for _, p := range backend.Placements() {
		if p.RootPath == "" || p.RootName == "" || dirExists(p.RootPath) {
			continue
		}
		r, ok := configs.RootNamed(p.RootName)
		if !ok || r.Path == p.RootPath || !dirExists(r.Path) {
			continue
		}
		// A name pointed at another folder isn't proof the files moved with it: re-pointed with no
		// files there, the torrent would download everything again into the new folder. It waits for
		// its own folder instead, like adoptMovedStaging.
		if !placedFilesAt(p, r.Path) {
			if _, logged := rootsWaiting.LoadOrStore(p.Hash, true); !logged {
				logger.Logger.Warn().Str("hash", p.Hash).Str("download_folder", p.RootName).Str("path", p.RootPath).
					Msg("The download folder this torrent was placed in is gone, and its files are not in the folder the name points at now; it waits for it to come back")
			}
			continue
		}
		rootsWaiting.Delete(p.Hash)
		to := r.Path
		if to == configs.CompletedAnimePath {
			to = "" // the library stays implicit (decisions/136)
		}
		if err := backend.MovePlacementRoot(p.Hash, to); err != nil {
			logger.Logger.Warn().Err(err).Str("hash", p.Hash).Msg("Failed to follow a moved download folder")
			continue
		}
		logger.Logger.Info().Str("hash", p.Hash).Str("path", r.Path).Msg("Followed a moved download folder")
	}
}

// rootsWaiting holds the hashes followMovedRoots already said are waiting, so the log says it once
// per run, not once per pass.
var rootsWaiting sync.Map

// placedFilesAt reports whether a placement's files are under root, the folder its download root's
// name points at now. A placement with no files yet, or still downloading into the incomplete
// folder, has nothing in any root, so there is nothing to check and it may follow. Otherwise one
// file the daemon wants, at its final name or (still downloading with part files) its ".part"
// name, is enough. p comes resolved, its paths under its old RootPath.
func placedFilesAt(p torrents.Placement, root string) bool {
	if len(p.Files) == 0 || (p.IncompleteRoot != "" && !p.Completed) {
		return true
	}
	for _, f := range p.Files {
		if f.Skipped {
			continue
		}
		rel, err := filepath.Rel(p.RootPath, f.Path)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		path := filepath.Join(root, rel)
		if fileExists(path) || (p.PartFiles && !p.Completed && fileExists(path+".part")) {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// adoptMovedStaging follows a moved incomplete folder (decisions/141): an unfinished staging
// torrent whose frozen root is gone, while its <hash> folder sits in the configured incomplete_path,
// is re-pointed there — the user moved the folder and changed the setting, as with a moved library
// (decisions/034). The torrent is added back, so rain re-checks the moved files before trusting
// them. One whose files are nowhere to be found waits for its root to come back, and the pass says
// so in the log.
func adoptMovedStaging(backend torrents.TorrentBackend, staging string) {
	for _, p := range backend.Placements() {
		if p.Completed || p.IncompleteRoot == "" || p.IncompleteRoot == staging || dirExists(p.IncompleteRoot) {
			continue
		}
		if staging != "" && dirExists(filepath.Join(staging, p.Hash)) {
			if err := backend.MovePlacementStaging(p.Hash, staging); err != nil {
				logger.Logger.Warn().Err(err).Str("hash", p.Hash).Msg("Failed to follow a moved incomplete downloads folder")
				continue
			}
			logger.Logger.Info().Str("hash", p.Hash).Str("incomplete_path", staging).Msg("Found a download in the moved incomplete folder; it continues from there")
			continue
		}
		logger.Logger.Warn().Str("hash", p.Hash).Str("incomplete_path", p.IncompleteRoot).
			Msg("The incomplete folder this download was started in is gone; it waits for it to come back")
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// pruneStagingLeftovers removes what torrents left in the incomplete folders (decisions/141): a
// <hash> folder whose placement is gone, or is Completed (moved; a crash or a failed cleanup came
// between the switch and the removal). A Kept placement's folder stays (its files are the user's,
// a re-add resumes into them), and so does any unfinished one's, whatever root it was registered
// with: a folder the user moved holds that torrent's data until adoptMovedStaging re-points it.
// Only folders named like an infohash are touched, and only after orphanPlacementGrace since they
// last changed. The roots are the configured incomplete_path and every root a placement points at.
func pruneStagingLeftovers(backend torrents.TorrentBackend, staging string, now time.Time) {
	roots := map[string]bool{}
	if staging != "" {
		roots[staging] = true
	}
	byHash := map[string]torrents.Placement{}
	for _, p := range backend.Placements() {
		byHash[p.Hash] = p
		if p.IncompleteRoot != "" {
			roots[p.IncompleteRoot] = true
		}
	}
	for root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !isInfoHash(e.Name()) {
				continue
			}
			if p, ok := byHash[e.Name()]; ok && (p.Kept || !p.Completed) {
				continue
			}
			info, err := e.Info()
			if err != nil || now.Sub(info.ModTime()) < orphanPlacementGrace {
				continue
			}
			dir := filepath.Join(root, e.Name())
			if err := os.RemoveAll(dir); err != nil {
				logger.Logger.Warn().Err(err).Str("hash", e.Name()).Msg("Failed to delete a leftover in the incomplete downloads folder")
				continue
			}
			logger.Logger.Info().Str("hash", e.Name()).Msg("Deleted a leftover in the incomplete downloads folder")
		}
	}
}

// isInfoHash reports whether name is a v1 infohash as the session writes it: 40 lowercase hex.
func isInfoHash(name string) bool {
	if len(name) != 40 {
		return false
	}
	for _, c := range name {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// statFile is os.Stat, swappable in tests.
var statFile = os.Stat

// PlacedFilesMissing reports whether a finished placement is missing any of its files — video,
// subtitle, anything: exactly the files placedStorage.Open refuses to recreate (decisions/139).
// The torrents list flags its torrent with it, and the failure handler and organize leave such a
// torrent alone.
func PlacedFilesMissing(p torrents.Placement) bool {
	if !p.Completed || !filepath.IsAbs(p.SeriesDir) {
		return false
	}
	for _, f := range p.Files {
		// A skipped file is one the daemon doesn't want: gone is no loss (decisions/153).
		if f.Skipped {
			continue
		}
		if _, err := statFile(p.DiskPath(f)); err != nil {
			return true
		}
	}
	return false
}

// RedownloadResult is what RedownloadMissingFiles did.
type RedownloadResult struct {
	// Redownloaded is how many torrents were restarted to fetch their missing files.
	Redownloaded int `json:"redownloaded" example:"3"`
	// SkippedNoSpace is how many were left as they were because their missing files don't fit
	// (the disk guard, min_free_disk_gb).
	SkippedNoSpace int `json:"skipped_no_space" example:"0"`
}

// RedownloadMissingFiles is the bulk "redownload missing files": every finished direct-mode
// torrent whose library files are gone (deleted outside the app, or the library folder was
// replaced) downloads them again into the same paths. It is the user's explicit action — nothing
// automatic ever does this (decisions/139). The disk guard counts each torrent's missing bytes on
// top of what the session's downloads still have to write (decisions/101); a torrent that doesn't
// fit is skipped, not half-started. saved is the episode records, for the sizes of queued
// torrents without metadata.
//
// only narrows it to those hashes (the user unticked some in the dialog); empty means every
// torrent with missing files.
func RedownloadMissingFiles(configs *files.Config, backend torrents.TorrentBackend, saved []files.EpisodeStruct, only []string) (RedownloadResult, error) {
	var result RedownloadResult
	var errs []error
	budget := newDiskBudget(backend.List(), saved).withRoots(configs, backend, nil)
	wanted := make(map[string]bool, len(only))
	for _, h := range only {
		wanted[strings.ToLower(h)] = true
	}
	for _, p := range backend.Placements() {
		if len(wanted) > 0 && !wanted[p.Hash] {
			continue
		}
		if _, inSession := backend.Get(p.Hash); !inSession || !PlacedFilesMissing(p) {
			continue
		}
		missing := missingBytes(backend, p)
		if err := budget.checkOn(configs, p.RootOr(configs.CompletedAnimePath), missing); err != nil {
			logger.Logger.Warn().Err(err).Str("hash", p.Hash).Msg("Redownload missing files: not enough disk space for this torrent")
			result.SkippedNoSpace++
			continue
		}
		if err := backend.RedownloadPlacement(p.Hash, configs.StagingPath()); err != nil {
			errs = append(errs, fmt.Errorf("torrent %s: %w", p.Hash, err))
			continue
		}
		budget.addOn(p.RootOr(configs.CompletedAnimePath), missing)
		result.Redownloaded++
	}
	return result, errors.Join(errs...)
}

// missingBytes sums the sizes of a placement's files that are gone, from the torrent's file list.
func missingBytes(backend torrents.TorrentBackend, p torrents.Placement) int64 {
	list, err := backend.Files(p.Hash)
	if err != nil {
		return 0
	}
	sizes := make(map[string]int64, len(list))
	for _, f := range list {
		sizes[f.Path] = f.Size
	}
	var total int64
	for _, f := range p.Files {
		if f.Skipped {
			continue
		}
		if _, err := statFile(f.Path); err != nil {
			total += sizes[f.Torrent]
		}
	}
	return total
}
