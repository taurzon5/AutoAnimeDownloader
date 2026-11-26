package api

import (
	"AutoAnimeDownloader/src/internal/daemon"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/torrents"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// torrentClientTestRequest carries the settings being edited, so the web UI can test them before
// saving. Every field is optional: an empty body tests the saved config, and a secret sent as
// files.SecretPlaceholder (what GET /config shows) uses the saved one.
type torrentClientTestRequest struct {
	CompletedAnimePath  string              `json:"completed_anime_path"`
	IncompletePath      string              `json:"incomplete_path"`
	QBittorrentURL      string              `json:"qbittorrent_url"`
	QBittorrentUsername string              `json:"qbittorrent_username"`
	QBittorrentPassword string              `json:"qbittorrent_password"`
	QBittorrentAPIKey   string              `json:"qbittorrent_api_key"`
	QBittorrentPathMap  []files.PathMapping `json:"qbittorrent_path_map"`
	// DownloadRoots and LibraryMode, as edited: the test also checks each download folder.
	DownloadRoots []files.DownloadRoot `json:"download_roots"`
	LibraryMode   string               `json:"library_mode"`
}

// handleTorrentClientTest is POST /api/v1/torrent-client/test: signs in to qBittorrent, reads its
// versions and checks that it sees the library (a probe file the app writes there). Nothing is
// saved and no torrent is touched.
func handleTorrentClientTest(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			JSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only POST method is allowed")
			return
		}
		saved, err := server.FileManager.LoadConfigs()
		if err != nil {
			JSONInternalError(w, err)
			return
		}
		config := *saved
		var req torrentClientTestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			JSONError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON format")
			return
		}
		if req.QBittorrentURL != "" {
			config.CompletedAnimePath = req.CompletedAnimePath
			config.IncompletePath = req.IncompletePath
			config.QBittorrentURL = req.QBittorrentURL
			config.QBittorrentUsername = req.QBittorrentUsername
			config.QBittorrentPassword = req.QBittorrentPassword
			config.QBittorrentAPIKey = req.QBittorrentAPIKey
			config.QBittorrentPathMap = req.QBittorrentPathMap
			if req.LibraryMode != "" {
				config.LibraryMode = req.LibraryMode
				config.DownloadRoots = req.DownloadRoots
			}
			if err := config.KeepSecrets(saved); err != nil {
				JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
				return
			}
		}
		if strings.TrimSpace(config.QBittorrentURL) == "" {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "qBittorrent URL is empty")
			return
		}
		if msg := validatePathMap(config.QBittorrentPathMap); msg != "" {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", msg)
			return
		}

		result, err := torrents.TestQBittorrent(daemon.QBitSettingsFrom(&config), config.CompletedAnimePath, config.StagingPath(), qbitTestFolders(&config))
		if err != nil {
			logger.Logger.Info().Err(err).Msg("qBittorrent connection test failed")
			JSONError(w, http.StatusBadGateway, "TORRENT_CLIENT_UNREACHABLE", err.Error())
			return
		}
		JSONSuccess(w, http.StatusOK, result)
	}
}

// qbitTestFolders is every download folder the connection test checks besides the library: the
// roots of direct mode, where qBittorrent saves torrents too (decisions/154).
func qbitTestFolders(c *files.Config) []torrents.QBitFolder {
	if !c.IsDirectMode() {
		return nil
	}
	var out []torrents.QBitFolder
	for _, r := range c.Roots() {
		out = append(out, torrents.QBitFolder{Name: r.Name, Path: r.Path})
	}
	return out
}
