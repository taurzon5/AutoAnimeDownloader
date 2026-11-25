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
	// Root is the download root this anime's new torrents go to first (decisions/154): a root's
	// name, or "" to drop the override. Absent leaves it as it is.
	Root *string `json:"root"`
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
			if req.Root != nil && *req.Root != "" {
				configs, err := server.FileManager.LoadConfigs()
				if err != nil {
					JSONInternalError(w, err)
					return
				}
				if _, ok := configs.RootNamed(*req.Root); !ok {
					JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "No download folder is named "+strconv.Quote(*req.Root))
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
			if req.Root != nil {
				settings.Root = nil
				if *req.Root != "" {
					root := *req.Root
					settings.Root = &root
				}
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
