package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/daemon"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
)

type StandaloneAnimeRequest struct {
	MediaID   int    `json:"media_id" example:"21"`
	Magnet    string `json:"magnet,omitempty"`
	Name      string `json:"name,omitempty"`
	NyaaID    int    `json:"nyaa_id,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	// SkipFiles are the torrent files the user unticked in the picker (paths from Nyaa's file
	// list), which qBittorrent never downloads. Only with a magnet, and only with qBittorrent:
	// 409 FILE_SELECTION_UNSUPPORTED otherwise (decisions/165).
	SkipFiles []string `json:"skip_files,omitempty"`
	// Unblacklist takes the anime off the app's blacklist and adds it. Without it a blacklisted
	// anime is refused with 409 BLACKLISTED, so the screen can ask first (decisions/155).
	Unblacklist bool `json:"unblacklist,omitempty"`
	// Queue is the download queue the anime goes in (decisions/164): a queue id; empty = the default
	// queue. Set before the first search, so that search ranks with the queue's priorities.
	Queue string `json:"queue,omitempty"`
}

type StandaloneAnimeAddResponse struct {
	// Added is how many episodes entered the queue now. 0 is a normal response: a
	// NOT_YET_RELEASED anime (or one whose next episode has not aired) is still tracked.
	// The toast translates that, not an error code.
	Added int `json:"added" example:"3"`
}

// blockReasonToErrorCode maps the block reason to the 409 error code. Same three reasons
// the search returns in block_reason.
var blockReasonToErrorCode = map[string]string{
	blockReasonStandalone: "ALREADY_STANDALONE",
	blockReasonTracked:    "ALREADY_TRACKED",
	blockReasonDownloaded: "ALREADY_DOWNLOADED",
}

func handleStandaloneAnimeAdd(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			JSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only POST method is allowed")
			return
		}

		var body StandaloneAnimeRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.MediaID <= 0 {
			JSONError(w, http.StatusBadRequest, "INVALID_BODY", "Body must be {\"media_id\": <positive int>}")
			return
		}

		config, err := server.FileManager.LoadConfigs()
		if err != nil {
			logger.Logger.Error().Err(err).Msg("Failed to load config")
			JSONInternalError(w, err)
			return
		}

		// First because it does not depend on the network, and because without a library
		// the Ensure below would fail and the user would get an opaque 500 instead of the cause.
		if config.DownloadPath() == "" {
			JSONError(w, http.StatusConflict, "LIBRARY_NOT_CONFIGURED",
				"Configure the completed anime path before adding animes")
			return
		}

		// Also what validates that the id exists and is ANIME, and where the episode total
		// that blockReason needs comes from.
		media, err := anilist.GetMediaByID(body.MediaID, anilist.PriorityCritical)
		// Local cache serves whoever ALREADY went through the app (reactivating a removed
		// standalone). Anyone who never did has no stored entry and still hits the error —
		// this is the only screen that legitimately needs AniList up.
		if err != nil && !errors.Is(err, anilist.ErrFromCache) {
			logger.Logger.Error().Err(err).Int("media_id", body.MediaID).Msg("Failed to fetch media from AniList")
			JSONInternalError(w, err)
			return
		}
		if media == nil {
			JSONError(w, http.StatusNotFound, "ANIME_NOT_FOUND", "AniList does not know this media id")
			return
		}
		totalEpisodes := 0
		if media.Media.Episodes != nil {
			totalEpisodes = *media.Media.Episodes
		}

		guard, err := newStandaloneGuard(server.FileManager, config)
		if err != nil {
			logger.Logger.Error().Err(err).Msg("Failed to build the standalone guard")
			JSONInternalError(w, err)
			return
		}
		if reason := guard.blockReason(body.MediaID, totalEpisodes); reason != "" {
			JSONError(w, http.StatusConflict, blockReasonToErrorCode[reason], standaloneBlockMessage(reason))
			return
		}

		// The app's blacklist asks instead of blocking: adding is the later choice, but taking an
		// anime off the blacklist is the user's to confirm (decisions/155). It is lifted only once
		// the anime is tracked, so a refused add leaves it blacklisted, like the id (decisions/108).
		// markAdded keeps the anime in the queue the add chose once the add went through.
		markAdded := func() {}
		blacklisted := loadAppBlacklist(server.FileManager)[body.MediaID]
		if blacklisted && !body.Unblacklist {
			JSONError(w, http.StatusConflict, "BLACKLISTED", "This anime is on the blacklist: confirm taking it off to add it")
			return
		}
		// The queue goes in first: the downloads below search with its ranking. An unknown queue is
		// refused before anything is tracked, and a refused add puts the anime back in the queue it
		// was in, as it leaves no id (decisions/108).
		if body.Queue != "" {
			store := daemon.QueueStore()
			if store == nil {
				JSONError(w, http.StatusServiceUnavailable, "QUEUES_UNAVAILABLE", "Download queues are not available")
				return
			}
			before := files.DefaultQueueID
			if list, err := store.All(); err == nil {
				_, q := files.QueueOf(list, body.MediaID)
				before = q.ID
			}
			if err := moveAnimeToQueue(store, body.MediaID, body.Queue); err != nil {
				writeQueueError(w, err)
				return
			}
			added := false
			defer func() {
				if added || before == body.Queue {
					return
				}
				if err := moveAnimeToQueue(store, body.MediaID, before); err != nil {
					logger.Logger.Warn().Err(err).Int("media_id", body.MediaID).Msg("Failed to put a refused add back in its queue")
				}
			}()
			markAdded = func() { added = true }
		}

		liftBlacklist := func() {
			if !blacklisted {
				return
			}
			if _, err := daemon.LiftBlacklist(server.FileManager, server.Torrents, body.MediaID); err != nil {
				logger.Logger.Warn().Err(err).Int("media_id", body.MediaID).Msg("Added the anime but failed to take it off the blacklist")
			}
		}

		if body.Magnet == "" && len(body.SkipFiles) > 0 {
			JSONError(w, http.StatusBadRequest, "INVALID_SKIP_FILES", "skip_files needs a magnet: a search picks its own torrents")
			return
		}
		if body.Magnet != "" {
			if !strings.HasPrefix(body.Magnet, "magnet:") {
				JSONError(w, http.StatusBadRequest, "INVALID_MAGNET", "Invalid or missing magnet link")
				return
			}
			// DownloadStandaloneAnimeWithMagnet writes the standalone id after the torrent
			// is accepted — the handler must not write it first.
			added, err := daemon.DownloadStandaloneAnimeWithMagnet(
				server.FileManager, server.Torrents, config,
				body.MediaID, body.Magnet,
				daemon.PickedRelease{Name: body.Name, NyaaID: body.NyaaID, SizeBytes: body.SizeBytes, Skip: body.SkipFiles},
			)
			if err != nil {
				logger.Logger.Error().Err(err).Int("media_id", body.MediaID).Msg("Failed to download the standalone anime")
				JSONDownloadError(w, err, "STANDALONE_DOWNLOAD_FAILED")
				return
			}
			liftBlacklist()
			markAdded()
			JSONSuccess(w, http.StatusOK, StandaloneAnimeAddResponse{Added: added})
			return
		}

		if err := server.FileManager.AddStandaloneAnime(body.MediaID); err != nil {
			logger.Logger.Error().Err(err).Int("media_id", body.MediaID).Msg("Failed to save the standalone anime")
			JSONInternalError(w, err)
			return
		}
		// Tracked from here, whatever the download below does: the add happened.
		liftBlacklist()
		markAdded()

		// Synchronous, matching the episode-download POST: it is a single Nyaa search, and
		// returning the real number of queued episodes is worth more than an empty 202.
		added, err := daemon.DownloadStandaloneAnime(server.FileManager, server.Torrents, config, body.MediaID, server.State.Shoko())
		if err != nil {
			logger.Logger.Error().Err(err).Int("media_id", body.MediaID).Msg("Failed to download the standalone anime")
			JSONDownloadError(w, err, "STANDALONE_DOWNLOAD_FAILED")
			return
		}

		JSONSuccess(w, http.StatusOK, StandaloneAnimeAddResponse{Added: added})
	}
}

func standaloneBlockMessage(reason string) string {
	switch reason {
	case blockReasonStandalone:
		return "This anime is already tracked as standalone"
	case blockReasonTracked:
		return "This anime is already in one of your AniList lists"
	case blockReasonDownloaded:
		return "This anime is already fully downloaded"
	}
	return "This anime cannot be added"
}

func handleStandaloneAnimeRemove(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			JSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only DELETE method is allowed")
			return
		}

		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			JSONError(w, http.StatusBadRequest, "INVALID_ID", "Invalid anime ID")
			return
		}

		deleteEpisodes, err := parseBoolQueryParam(r, "delete_episodes")
		if err != nil {
			JSONError(w, http.StatusBadRequest, "INVALID_QUERY_PARAM", err.Error())
			return
		}

		if _, err := removeStandaloneAnime(server, id, deleteEpisodes); err != nil {
			logger.Logger.Error().Err(err).Int("media_id", id).Msg("Failed to remove the standalone anime")
			JSONInternalError(w, err)
			return
		}

		JSONSuccess(w, http.StatusOK, nil)
	}
}

// removeStandaloneAnime stops tracking an anime added by hand and returns how many episodes it
// deleted. With deleteEpisodes its downloaded episodes go with their files; without, the files stay
// and ManuallyManaged is what protects them: without the standalone record the anime drops out of
// the pass, so its episodes would hit identifyEpisodesNotInWatching on the next cycle and vanish
// from disk unasked. Shared by DELETE /standalone-animes/{id} and DELETE /animes/{id}.
func removeStandaloneAnime(server *Server, id int, deleteEpisodes bool) (int, error) {
	if err := server.FileManager.RemoveStandaloneAnime(id); err != nil {
		return 0, err
	}
	clearAnimeBlocks(server.FileManager, id)

	saved, err := server.FileManager.LoadSavedEpisodes()
	if err != nil {
		return 0, err
	}
	var group []files.EpisodeStruct
	var keys []files.EpisodeKey
	for _, ep := range saved {
		if ep.AnimeID == id {
			group = append(group, ep)
			keys = append(keys, ep.Key())
		}
	}

	if deleteEpisodes {
		if err := daemon.RemoveEpisodesWithLinks(server.FileManager, server.Torrents, server.Librarian, keys); err != nil {
			return 0, err
		}
		return len(keys), nil
	}
	for i := range group {
		group[i].ManuallyManaged = true
	}
	return 0, server.FileManager.UpsertEpisodes(group)
}

// clearAnimeBlocks drops every blocked episode of an anime the user stopped tracking. Deleting an
// episode always blocks it — that is how the loop is kept from downloading it again — so the
// blocks are bookkeeping of the tracking, not a preference: left behind, they only meant that
// adding the anime again silently never downloaded those episodes. Only this endpoint does it: the
// pass also drops a standalone id when the anime shows up in an AniList list, and there the
// anime is still tracked, so its blocks must stay. Best effort — a failure is logged, not returned.
func clearAnimeBlocks(fm FileManagerInterface, mediaID int) {
	blocked, err := fm.LoadBlockedEpisodes()
	if err != nil {
		logger.Logger.Warn().Err(err).Int("media_id", mediaID).Msg("Failed to load blocked episodes to clear them")
		return
	}
	for _, key := range blocked {
		if key.AnimeID != mediaID {
			continue
		}
		if err := fm.UnblockEpisode(key); err != nil {
			logger.Logger.Warn().Err(err).Int("media_id", mediaID).Int("episode", key.Episode).Msg("Failed to unblock episode of a removed standalone anime")
		}
	}
}
