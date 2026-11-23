package daemon

import (
	"fmt"
	"path/filepath"
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
func registerPlacement(configs *files.Config, backend torrents.TorrentBackend, magnet string, target torrentTarget) error {
	if !configs.IsDirectMode() {
		return nil
	}
	hash, err := torrents.InfoHash(magnet)
	if err != nil {
		return nil
	}
	req := placementRequest(configs, hash, target)
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
