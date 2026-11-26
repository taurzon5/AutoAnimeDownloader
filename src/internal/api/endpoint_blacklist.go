package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/daemon"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
)

// The app's blacklist and the anime-wide delete (decisions/155). The blacklist lives in each
// anime's settings (files.AnimeSettings.Blacklisted); the daemon skips those anime in the pass.

// BlacklistEntry is one anime on the app's blacklist, as the Blacklist page shows it.
type BlacklistEntry struct {
	AnimeID int    `json:"anime_id" example:"21"`
	Name    string `json:"name" example:"One Piece"`
	Cover   string `json:"cover,omitempty"`
	Format  string `json:"format,omitempty" example:"TV"`
	// BlacklistedAt is when it went on the blacklist (RFC 3339).
	BlacklistedAt string `json:"blacklisted_at" example:"2026-11-25T22:13:00Z"`
	// Paused is how many unfinished torrents the blacklist paused; lifting it resumes them.
	Paused int `json:"paused"`
	// Episodes is how many downloaded episodes the app still has a record of; their files stay.
	Episodes int `json:"episodes"`
}

// BlacklistAddRequest is the body of POST /api/v1/blacklist.
type BlacklistAddRequest struct {
	MediaID int `json:"media_id" example:"21"`
}

// AnimeDeleteResponse says what DELETE /api/v1/animes/{id} did.
type AnimeDeleteResponse struct {
	// Removed: the anime was added by hand and is no longer tracked.
	Removed bool `json:"removed"`
	// DeletedEpisodes is how many downloaded episodes were deleted with their files.
	DeletedEpisodes int `json:"deleted_episodes"`
	// Blacklisted: the anime is on the app's blacklist now.
	Blacklisted bool `json:"blacklisted"`
}

// loadAppBlacklist is the set of anime on the app's blacklist. A failed read is an empty set,
// logged: the screens then just don't mark them.
func loadAppBlacklist(fm FileManagerInterface) map[int]bool {
	all, err := fm.LoadAllAnimeSettings()
	if err != nil {
		logger.Logger.Warn().Err(err).Msg("Failed to load anime settings to read the blacklist")
		return map[int]bool{}
	}
	return files.BlacklistedIDs(all)
}

// blacklistInfoFor reads the name, cover and format the Blacklist page keeps for an anime. AniList
// unreachable and nothing cached: an empty info, and the page shows the id.
func blacklistInfoFor(id int) daemon.BlacklistInfo {
	ml, err := anilist.GetMediaByID(id, anilist.PriorityCritical)
	if (err != nil && !errors.Is(err, anilist.ErrFromCache)) || ml == nil {
		logger.Logger.Warn().Err(err).Int("media_id", id).Msg("Blacklist: no AniList details for the anime; keeping it without a name")
		return daemon.BlacklistInfo{}
	}
	cover := ml.Media.CoverImage.Large
	if cover == "" {
		cover = ml.Media.CoverImage.Medium
	}
	return daemon.BlacklistInfo{Name: resolveTitle(ml.Media.Title), Cover: cover, Format: string(ml.Media.Format)}
}

// handleBlacklist serves GET (the list) and POST (blacklist an anime) on /api/v1/blacklist.
func handleBlacklist(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			all, err := server.FileManager.LoadAllAnimeSettings()
			if err != nil {
				logger.Logger.Error().Err(err).Msg("Failed to load anime settings")
				JSONInternalError(w, err)
				return
			}
			saved, err := server.FileManager.LoadSavedEpisodes()
			if err != nil {
				logger.Logger.Error().Err(err).Msg("Failed to load saved episodes")
				JSONInternalError(w, err)
				return
			}
			episodes := make(map[int]int)
			for _, ep := range saved {
				episodes[ep.AnimeID]++
			}
			out := make([]BlacklistEntry, 0)
			for id, s := range all {
				b := s.Blacklisted
				if b == nil {
					continue
				}
				out = append(out, BlacklistEntry{
					AnimeID: id, Name: b.Name, Cover: b.Cover, Format: b.Format,
					BlacklistedAt: b.At.UTC().Format(time.RFC3339),
					Paused:        len(b.Paused), Episodes: episodes[id],
				})
			}
			// Newest first: the one just blacklisted is the one the user is looking for.
			sort.Slice(out, func(i, j int) bool {
				if out[i].BlacklistedAt != out[j].BlacklistedAt {
					return out[i].BlacklistedAt > out[j].BlacklistedAt
				}
				return out[i].AnimeID < out[j].AnimeID
			})
			JSONSuccess(w, http.StatusOK, out)

		case http.MethodPost:
			var body BlacklistAddRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.MediaID <= 0 {
				JSONError(w, http.StatusBadRequest, "INVALID_BODY", "Body must be {\"media_id\": <positive int>}")
				return
			}
			if _, err := daemon.BlacklistAnime(server.FileManager, server.Torrents, body.MediaID, blacklistInfoFor(body.MediaID), time.Now()); err != nil {
				logger.Logger.Error().Err(err).Int("media_id", body.MediaID).Msg("Failed to blacklist the anime")
				JSONInternalError(w, err)
				return
			}
			JSONSuccess(w, http.StatusOK, nil)

		default:
			JSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET and POST methods are allowed")
		}
	}
}

// handleBlacklistRemove takes an anime off the app's blacklist: DELETE /api/v1/blacklist/{id}.
func handleBlacklistRemove(server *Server) http.HandlerFunc {
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
		lifted, err := daemon.LiftBlacklist(server.FileManager, server.Torrents, id)
		if err != nil {
			logger.Logger.Error().Err(err).Int("media_id", id).Msg("Failed to take the anime off the blacklist")
			JSONInternalError(w, err)
			return
		}
		if !lifted {
			JSONError(w, http.StatusNotFound, "NOT_BLACKLISTED", "This anime is not on the blacklist")
			return
		}
		JSONSuccess(w, http.StatusOK, nil)
	}
}

// handleAnimeDelete is DELETE /api/v1/animes/{id}?delete_files=<bool>&blacklist=<bool>. An anime
// added by hand is removed (its files deleted with delete_files, kept otherwise). One a list brings
// can't be removed here, the list would bring it back: delete_files deletes its downloaded episodes
// and blocks them, blacklist stops the app from checking and downloading it. A list anime with
// neither is refused (409 ON_A_LIST).
func handleAnimeDelete(server *Server) http.HandlerFunc {
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
		deleteFiles, err := parseBoolQueryParam(r, "delete_files")
		if err != nil {
			JSONError(w, http.StatusBadRequest, "INVALID_QUERY_PARAM", err.Error())
			return
		}
		blacklist, err := parseBoolQueryParam(r, "blacklist")
		if err != nil {
			JSONError(w, http.StatusBadRequest, "INVALID_QUERY_PARAM", err.Error())
			return
		}

		standalone := loadStandaloneSet(server.FileManager)[id]
		if !standalone && !deleteFiles && !blacklist {
			JSONError(w, http.StatusConflict, "ON_A_LIST",
				"This anime comes from a list, so the app can't remove it: take it off the list on AniList or MyAnimeList, or delete its files or blacklist it here")
			return
		}

		var resp AnimeDeleteResponse
		if standalone {
			n, err := removeStandaloneAnime(server, id, deleteFiles)
			if err != nil {
				logger.Logger.Error().Err(err).Int("media_id", id).Msg("Failed to remove the standalone anime")
				JSONInternalError(w, err)
				return
			}
			resp.Removed, resp.DeletedEpisodes = true, n
		} else if deleteFiles {
			n, err := deleteListAnimeEpisodes(server, id)
			if err != nil {
				logger.Logger.Error().Err(err).Int("media_id", id).Msg("Failed to delete the anime's episodes")
				JSONInternalError(w, err)
				return
			}
			resp.DeletedEpisodes = n
		}

		// After the files: what was just deleted has nothing left to pause.
		if blacklist {
			if _, err := daemon.BlacklistAnime(server.FileManager, server.Torrents, id, blacklistInfoFor(id), time.Now()); err != nil {
				logger.Logger.Error().Err(err).Int("media_id", id).Msg("Failed to blacklist the anime")
				JSONInternalError(w, err)
				return
			}
			resp.Blacklisted = true
		}
		JSONSuccess(w, http.StatusOK, resp)
	}
}

// deleteListAnimeEpisodes deletes every downloaded episode of an anime a list brings, files
// included, and blocks them so the next pass doesn't download them again: what DELETE
// /animes/{id}/episodes/{n} does, for all of them in one go (a pack they share is freed).
func deleteListAnimeEpisodes(server *Server, id int) (int, error) {
	saved, err := server.FileManager.LoadSavedEpisodes()
	if err != nil {
		return 0, err
	}
	var keys []files.EpisodeKey
	for _, ep := range saved {
		if ep.AnimeID == id {
			keys = append(keys, ep.Key())
		}
	}
	if err := daemon.RemoveEpisodesWithLinks(server.FileManager, server.Torrents, server.Librarian, keys); err != nil {
		return 0, err
	}
	for _, k := range keys {
		if err := server.FileManager.BlockEpisode(k); err != nil {
			logger.Logger.Warn().Err(err).Int("media_id", id).Int("episode", k.Episode).Msg("Failed to block a deleted episode")
		}
	}
	return len(keys), nil
}
