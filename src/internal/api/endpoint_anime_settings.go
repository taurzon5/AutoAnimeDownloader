package api

import (
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"encoding/json"
	"net/http"
	"strconv"
)

// Progress is a POINTER because the PUT is partial: absent must be distinguishable from
// zero, or a body without the field would wipe the saved progress.
type animeSettingsRequest struct {
	Progress *int `json:"progress"`
	// Shoko sets the Shoko match by hand (decisions/146): {"series_id": 3, "offset": 12}, or
	// {"none": true} for "not in Shoko", or {"auto": true} to go back to the match worked out from
	// the MAL id. Absent leaves it as it is.
	Shoko *ShokoMatchRequest `json:"shoko"`
}

// ShokoMatchRequest is the "shoko" part of PUT /animes/{id}/settings: exactly one of a series
// (with its offset), None or Auto.
type ShokoMatchRequest struct {
	SeriesID int  `json:"series_id"`
	Offset   int  `json:"offset"`
	None     bool `json:"none"`
	Auto     bool `json:"auto"`
}

func handleAnimeSettings(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.Atoi(idStr)
		if err != nil || id <= 0 {
			JSONError(w, http.StatusBadRequest, "INVALID_ID", "Invalid anime ID")
			return
		}

		switch r.Method {
		case http.MethodGet:
			settings, err := server.FileManager.LoadAnimeSettings(id)
			if err != nil {
				logger.Logger.Error().Err(err).Int("anime_id", id).Msg("Failed to load anime settings")
				JSONInternalError(w, err)
				return
			}
			JSONSuccess(w, http.StatusOK, settings)

		case http.MethodPut:
			var req animeSettingsRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				JSONError(w, http.StatusBadRequest, "INVALID_BODY", "Invalid request body")
				return
			}
			if req.Progress != nil && *req.Progress < 0 {
				JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Progress must be non-negative")
				return
			}
			if s := req.Shoko; s != nil {
				picks := 0
				for _, set := range []bool{s.SeriesID != 0, s.None, s.Auto} {
					if set {
						picks++
					}
				}
				if picks != 1 || s.SeriesID < 0 || s.Offset < 0 || (s.SeriesID == 0 && s.Offset != 0) {
					JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "shoko takes one of: a series_id (with an offset >= 0), none, or auto")
					return
				}
			}

			existing, err := server.FileManager.LoadAnimeSettings(id)
			if err != nil {
				logger.Logger.Error().Err(err).Int("anime_id", id).Msg("Failed to load anime settings")
				JSONInternalError(w, err)
				return
			}

			settings := files.AnimeSettings{}
			if existing != nil {
				settings = *existing
			}
			if req.Progress != nil {
				settings.Progress = *req.Progress
			}
			switch s := req.Shoko; {
			case s == nil:
			case s.Auto:
				settings.Shoko = nil
			case s.None:
				settings.Shoko = &files.ShokoMatch{None: true}
			default:
				settings.Shoko = &files.ShokoMatch{SeriesID: s.SeriesID, Offset: s.Offset}
			}

			if err := server.FileManager.SaveAnimeSettings(id, settings); err != nil {
				logger.Logger.Error().Err(err).Int("anime_id", id).Msg("Failed to save anime settings")
				JSONInternalError(w, err)
				return
			}

			JSONSuccess(w, http.StatusOK, nil)

		default:
			JSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET and PUT methods are allowed")
		}
	}
}
