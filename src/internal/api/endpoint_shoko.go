package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/shoko"
)

// shokoLoginRequest is POST /api/v1/shoko/login's body. The password is sent to Shoko once and
// kept nowhere: only the API key Shoko returns is saved (the Shoko spec's answer 1).
type shokoLoginRequest struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// ShokoStatus is GET /api/v1/shoko/status: whether Shoko is on and signed in, and what the last
// sync read.
type ShokoStatus struct {
	Enabled      bool       `json:"enabled"`
	URL          string     `json:"url,omitempty"`
	SignedIn     bool       `json:"signed_in"`
	LastSync     *time.Time `json:"last_sync,omitempty"`
	LastFullSync *time.Time `json:"last_full_sync,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	// Series is how many series Shoko has; OwnedEpisodes how many episodes of the series that match
	// an anime the app follows have a file there.
	Series        int `json:"series"`
	OwnedEpisodes int `json:"owned_episodes"`
	// Watching: connected to Shoko's event hub (shoko_watch), so changes apply within seconds;
	// LastEvent is Shoko's last file event, WatchError why the last connection failed.
	Watching   bool       `json:"watching"`
	LastEvent  *time.Time `json:"last_event,omitempty"`
	WatchError string     `json:"watch_error,omitempty"`
}

// timeOrNil leaves a zero time out of the JSON: omitempty doesn't omit a zero time.Time.
func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// handleShokoLogin signs in to Shoko and saves the address and the API key Shoko returns.
func handleShokoLogin(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			JSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only POST method is allowed")
			return
		}
		var req shokoLoginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			JSONError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON format")
			return
		}
		if strings.TrimSpace(req.URL) == "" || req.Username == "" {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "The Shoko address and username are required")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		key, err := shoko.Login(ctx, req.URL, req.Username, req.Password)
		switch {
		case errors.Is(err, shoko.ErrUnauthorized):
			JSONError(w, http.StatusBadRequest, "SHOKO_LOGIN_REFUSED", "Shoko refused the username or password")
			return
		case err != nil && strings.Contains(err.Error(), "http(s) address"):
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
			return
		case err != nil:
			logger.Logger.Info().Err(err).Msg("Shoko sign-in failed")
			JSONError(w, http.StatusBadGateway, "SHOKO_UNREACHABLE", err.Error())
			return
		}
		configs, err := server.FileManager.LoadConfigs()
		if err != nil {
			JSONInternalError(w, err)
			return
		}
		configs.ShokoURL = strings.TrimRight(strings.TrimSpace(req.URL), "/")
		configs.ShokoAPIKey = key
		if err := server.FileManager.SaveConfigs(configs); err != nil {
			JSONInternalError(w, err)
			return
		}
		logger.Logger.Info().Msg("Signed in to Shoko")
		JSONSuccess(w, http.StatusOK, map[string]string{"message": "Signed in to Shoko"})
	}
}

// handleShokoStatus reports the integration's state.
func handleShokoStatus(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			JSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET method is allowed")
			return
		}
		configs, err := server.FileManager.LoadConfigs()
		if err != nil {
			JSONInternalError(w, err)
			return
		}
		out := ShokoStatus{Enabled: configs.ShokoEnabled, URL: configs.ShokoURL, SignedIn: configs.ShokoAPIKey != ""}
		if svc := server.State.Shoko(); svc != nil {
			snap := svc.Store().Snapshot()
			out.LastSync, out.LastFullSync, out.LastError = timeOrNil(snap.LastSync), timeOrNil(snap.LastFullSync), snap.LastError
			out.Series = len(snap.Series)
			for _, eps := range snap.Owned {
				out.OwnedEpisodes += len(eps)
			}
			w := svc.WatchState()
			out.Watching, out.LastEvent, out.WatchError = w.Connected, timeOrNil(w.LastEvent), w.Error
		}
		JSONSuccess(w, http.StatusOK, out)
	}
}

// handleShokoSync starts a pass that syncs with Shoko first; with {"full": true} it reads all of
// Shoko again (the Full resync button: a newer-only refresh can't see a file deleted in Shoko).
func handleShokoSync(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			JSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only POST method is allowed")
			return
		}
		var req struct {
			Full bool `json:"full"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		svc := server.State.Shoko()
		if svc == nil {
			JSONError(w, http.StatusServiceUnavailable, "SHOKO_UNAVAILABLE", "The Shoko integration is not available")
			return
		}
		if req.Full {
			svc.RequestFullSync()
		}
		server.startCheck()
		JSONSuccess(w, http.StatusAccepted, map[string]any{"message": "Shoko sync started with a check pass", "full": req.Full})
	}
}
