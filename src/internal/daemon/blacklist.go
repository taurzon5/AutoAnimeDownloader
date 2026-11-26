package daemon

import (
	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/torrents"
	"fmt"
	"slices"
	"time"
)

// The app's blacklist (decisions/155): an anime on it is skipped by the pass as a whole and keeps
// its files. It lives in the anime's settings (AnimeSettings.Blacklisted), not in excluded_lists:
// that one is an AniList custom list and deletes what the daemon downloaded (decisions/121).

// BlacklistInfo is what the Blacklist page shows of an anime, kept with the blacklisting.
type BlacklistInfo struct {
	Name, Cover, Format string
}

// BlacklistAnime puts an anime on the app's blacklist and pauses its unfinished torrents. Already
// on it: nothing changes, and the first blacklisting's paused list is kept.
func BlacklistAnime(fm FileManagerInterface, backend torrents.TorrentBackend, animeID int, info BlacklistInfo, now time.Time) (*files.Blacklisting, error) {
	settings, err := fm.LoadAnimeSettings(animeID)
	if err != nil {
		return nil, fmt.Errorf("failed to load anime settings: %w", err)
	}
	if settings == nil {
		settings = &files.AnimeSettings{}
	}
	if settings.Blacklisted != nil {
		return settings.Blacklisted, nil
	}

	saved, err := fm.LoadSavedEpisodes()
	if err != nil {
		return nil, fmt.Errorf("failed to load saved episodes: %w", err)
	}
	paused := pauseAll(backend, unfinishedTorrentsOnlyOf(backend, saved, animeID), animeID)

	settings.Blacklisted = &files.Blacklisting{At: now, Name: info.Name, Cover: info.Cover, Format: info.Format, Paused: paused}
	if err := fm.SaveAnimeSettings(animeID, *settings); err != nil {
		return nil, fmt.Errorf("failed to save anime settings: %w", err)
	}
	logger.Logger.Info().Int("anime_id", animeID).Str("name", info.Name).Int("paused", len(paused)).Msg("Anime blacklisted")
	return settings.Blacklisted, nil
}

// LiftBlacklist takes an anime off the app's blacklist and resumes the torrents the blacklisting
// paused that are still there and unfinished. Not on it: false, nothing changes.
func LiftBlacklist(fm FileManagerInterface, backend torrents.TorrentBackend, animeID int) (bool, error) {
	settings, err := fm.LoadAnimeSettings(animeID)
	if err != nil {
		return false, fmt.Errorf("failed to load anime settings: %w", err)
	}
	if settings == nil || settings.Blacklisted == nil {
		return false, nil
	}
	paused := settings.Blacklisted.Paused
	settings.Blacklisted = nil
	if err := fm.SaveAnimeSettings(animeID, *settings); err != nil {
		return false, fmt.Errorf("failed to save anime settings: %w", err)
	}
	for _, hash := range paused {
		if t, ok := backend.Get(hash); !ok || t.Completed {
			continue
		}
		if err := backend.Resume(hash); err != nil {
			logger.Logger.Warn().Err(err).Str("hash", hash).Int("anime_id", animeID).Msg("Blacklist lifted: failed to resume the anime's torrent")
		}
	}
	logger.Logger.Info().Int("anime_id", animeID).Int("resumed", len(paused)).Msg("Anime taken off the blacklist")
	return true, nil
}

// pauseAll pauses the given torrents and returns the ones it paused.
func pauseAll(backend torrents.TorrentBackend, hashes []string, animeID int) []string {
	var paused []string
	for _, hash := range hashes {
		if err := backend.Pause(hash); err != nil {
			logger.Logger.Warn().Err(err).Str("hash", hash).Int("anime_id", animeID).Msg("Blacklist: failed to pause the anime's torrent")
			continue
		}
		paused = append(paused, hash)
	}
	return paused
}

// keepBlacklistedPaused pauses, at the start of each pass, any unfinished torrent of a blacklisted
// anime that is running again, and records it so lifting the blacklist resumes it. Two kinds get
// there after the click: a torrent the app had stopped and that starts on its own (waiting for its
// incomplete folder or download root, a qBittorrent torrent waiting for its renames), and one a pass
// already running added for the anime. A torrent the user paused never starts on its own, so it is
// never touched here (decisions/155).
func keepBlacklistedPaused(fm FileManagerInterface, backend torrents.TorrentBackend, saved []files.EpisodeStruct, blacklisted map[int]bool) {
	for id := range blacklisted {
		hashes := unfinishedTorrentsOnlyOf(backend, saved, id)
		if len(hashes) == 0 {
			continue
		}
		settings, err := fm.LoadAnimeSettings(id)
		if err != nil || settings == nil || settings.Blacklisted == nil {
			continue
		}
		paused := pauseAll(backend, hashes, id)
		if len(paused) == 0 {
			continue
		}
		for _, h := range paused {
			if !slices.Contains(settings.Blacklisted.Paused, h) {
				settings.Blacklisted.Paused = append(settings.Blacklisted.Paused, h)
			}
		}
		if err := fm.SaveAnimeSettings(id, *settings); err != nil {
			logger.Logger.Warn().Err(err).Int("anime_id", id).Msg("Blacklist: failed to record the torrents it paused again")
			continue
		}
		logger.Logger.Info().Int("anime_id", id).Int("paused", len(paused)).Msg("Blacklist: paused the anime's torrents that were running again")
	}
}

// unfinishedTorrentsOnlyOf lists the torrents of an anime still downloading or waiting in the queue,
// leaving out a pack that also holds another anime's episodes (pausing it would stop that anime too)
// and a stopped one: the user may have paused it, and lifting the blacklist must not resume it. One
// the app stopped and that starts again later is paused by keepBlacklistedPaused.
func unfinishedTorrentsOnlyOf(backend torrents.TorrentBackend, saved []files.EpisodeStruct, animeID int) []string {
	owners := make(map[string]map[int]bool)
	for _, ep := range saved {
		if ep.EpisodeHash == "" {
			continue
		}
		if owners[ep.EpisodeHash] == nil {
			owners[ep.EpisodeHash] = make(map[int]bool)
		}
		owners[ep.EpisodeHash][ep.AnimeID] = true
	}
	var hashes []string
	for hash, animes := range owners {
		if len(animes) != 1 || !animes[animeID] {
			continue
		}
		t, ok := backend.Get(hash)
		if !ok || t.Completed || t.Status == torrents.StatusStopped || t.Status == torrents.StatusStopping {
			continue
		}
		hashes = append(hashes, hash)
	}
	slices.Sort(hashes)
	return hashes
}

// blacklistedAnimes is the set of anime the pass skips. A failed read skips none for that pass, and
// says so in the log: the pass goes on rather than stopping every anime over one settings file.
func blacklistedAnimes(fm FileManagerInterface) map[int]bool {
	all, err := fm.LoadAllAnimeSettings()
	if err != nil {
		logger.Logger.Warn().Err(err).Msg("Failed to load anime settings; no anime is skipped as blacklisted this pass")
		return map[int]bool{}
	}
	return files.BlacklistedIDs(all)
}

// withoutAnimes drops the keys of the given anime from an automatic deletion: a blacklisted anime
// keeps its files, whatever the delete rules say (decisions/155).
func withoutAnimes(keys []files.EpisodeKey, skip map[int]bool) []files.EpisodeKey {
	if len(skip) == 0 || len(keys) == 0 {
		return keys
	}
	kept := keys[:0:0]
	for _, k := range keys {
		if !skip[k.AnimeID] {
			kept = append(kept, k)
		}
	}
	return kept
}

// withoutBlacklistedIDs drops blacklisted anime from a list of media ids.
func withoutBlacklistedIDs(ids []int, skip map[int]bool) []int {
	if len(skip) == 0 {
		return ids
	}
	kept := make([]int, 0, len(ids))
	for _, id := range ids {
		if !skip[id] {
			kept = append(kept, id)
		}
	}
	return kept
}

// withoutBlacklistedAnimes drops blacklisted anime from the pass's list.
func withoutBlacklistedAnimes(animes []anilist.MediaList, skip map[int]bool) []anilist.MediaList {
	if len(skip) == 0 {
		return animes
	}
	kept := make([]anilist.MediaList, 0, len(animes))
	for _, a := range animes {
		if !skip[a.Media.Id] {
			kept = append(kept, a)
		}
	}
	return kept
}
