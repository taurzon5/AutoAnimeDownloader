package api

import (
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

func handleConfig(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleGetConfig(server)(w, r)
		case http.MethodPut:
			handleUpdateConfig(server)(w, r)
		default:
			JSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET and PUT methods are allowed")
		}
	}
}

func handleGetConfig(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		configs, err := server.FileManager.LoadConfigs()
		if err != nil {
			logger.Logger.Error().Err(err).Msg("Failed to load configs")
			JSONInternalError(w, err)
			return
		}

		// The qBittorrent password and API key never leave the daemon (decisions/143).
		JSONSuccess(w, http.StatusOK, configs.MaskSecrets())
	}
}

func handleUpdateConfig(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var config files.Config
		if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
			logger.Logger.Error().Err(err).Msg("Failed to decode config JSON")
			JSONError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON format")
			return
		}

		// Read before anything else: a secret sent back as the placeholder keeps its saved value, and
		// disable_seeding/library_mode/torrent_client are compared with it below.
		previous, err := server.FileManager.LoadConfigs()
		if err != nil {
			logger.Logger.Error().Err(err).Msg("Failed to load configs")
			JSONInternalError(w, err)
			return
		}
		if err := config.KeepSecrets(previous); err != nil {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
			return
		}

		// The anilist_username -> anilist_usernames migration is NOT repeated here: LoadConfigs
		// migrates once, on write (decisions/090). A body that still sends the singular
		// key is persisted and cured on the next read.

		// anilist_usernames is NOT validated: an install that only uses standalone animes
		// never configures an account (decisions/049).

		if config.CompletedAnimePath == "" {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Completed anime path is required")
			return
		}

		switch config.LibraryMode {
		case "", files.LibraryModeHardlink, files.LibraryModeDirect:
		default:
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", `Library mode must be "hardlink" or "direct"`)
			return
		}
		switch config.IncompleteFiles {
		case "", files.IncompleteFilesPart, files.IncompleteFilesInPlace:
		default:
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", `Incomplete files must be "part" or "in_place"`)
			return
		}
		switch config.EpisodeNumbering {
		case "", files.EpisodeNumberingSeason, files.EpisodeNumberingEntry:
		default:
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", `Episode numbering must be "season" or "entry"`)
			return
		}
		if msg := validateTorrentClient(&config); msg != "" {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", msg)
			return
		}

		// The hardlink library needs hardlinks; not every filesystem supports them. Probe at
		// save time, with the same function the runtime uses. Direct mode links nothing, so it
		// only needs a library folder it can write to.
		if config.IsDirectMode() {
			if err := files.ProbeWritable(config.CompletedAnimePath); err != nil {
				JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
				return
			}
			// Hardlink mode ignores incomplete_path, so a stale value there doesn't block a save.
			if config.IncompletePath != "" {
				if err := files.ProbeIncompleteFolder(config.IncompletePath, config.CompletedAnimePath); err != nil {
					JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
					return
				}
			}
		} else if server.Librarian != nil {
			if err := server.Librarian.ProbePath(config.CompletedAnimePath); err != nil {
				JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
				return
			}
		}

		if config.CheckInterval <= 0 {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Check interval must be greater than 0")
			return
		}

		// max_episodes_per_anime accepts 0 = no cap, aligned with the project's other caps
		// (max_batch_torrent_size_gb, min_seeders, watched_episodes_to_keep).
		if config.MaxEpisodesPerAnime < 0 {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Max episodes per anime must be non-negative")
			return
		}

		if config.EpisodeRetryLimit < 0 {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Episode retry limit must be non-negative")
			return
		}

		if config.WatchedEpisodesToKeep < 0 {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Watched episodes to keep must be non-negative")
			return
		}

		if config.IncompleteMinFreeDiskGB < 0 {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Incomplete folder minimum free space must be non-negative")
			return
		}

		if config.MaxConcurrentDownloads < 0 {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Max concurrent downloads must be non-negative")
			return
		}

		if config.MaxBatchTorrentSizeGB < 0 || config.MaxEpisodeTorrentSizeGB < 0 {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Torrent size limits must be non-negative")
			return
		}

		if config.MinSeeders < 0 {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Min seeders must be non-negative")
			return
		}

		if config.MaxSearchPages < 0 {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Max search pages must be non-negative")
			return
		}

		if config.NyaaRequestDelaySeconds < 0 {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Nyaa request delay must be non-negative")
			return
		}

		if config.MinFreeDiskGB < 0 {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Min free disk space must be non-negative")
			return
		}

		if config.Notifications.BatchWindowSeconds < 0 {
			JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Notification batch window must be non-negative")
			return
		}

		if err := server.FileManager.SaveConfigs(&config); err != nil {
			logger.Logger.Error().Err(err).Msg("Failed to save configs")
			JSONInternalError(w, err)
			return
		}

		// Apply the limit immediately: without this, lowering concurrent downloads would
		// only take effect on the next check pass (10 min by default), which reads as
		// the field not working.
		if server.Torrents != nil {
			server.Torrents.SetMaxActiveDownloads(config.MaxConcurrentDownloads)
			// Same for disable_seeding: the session only picks it up when it is rebuilt, and waiting
			// for the next pass would keep uploading for up to check_interval after it was turned on.
			// Only when it changed: Ensure here would also apply a download path change, which is
			// the pass's job. An incomplete config has no session yet; the first pass builds it right.
			server.Torrents.SetUploadDisabled(config.DisableSeeding)
			// library_mode, the same way: rain reads its storage once, when the session opens.
			server.Torrents.SetDirectMode(config.IsDirectMode())
			modeChanged := previous == nil || previous.IsDirectMode() != config.IsDirectMode()
			// qBittorrent reads these two on Ensure: its upload floor and whether it sets its queue.
			qbitChanged := previous != nil && (previous.QBittorrentDisableSeedingUploadKiB != config.QBittorrentDisableSeedingUploadKiB ||
				previous.QBittorrentManageSettings != config.QBittorrentManageSettings)
			if path := config.DownloadPath(); path != "" && (previous == nil || previous.DisableSeeding != config.DisableSeeding || modeChanged || qbitChanged) {
				if _, err := server.Torrents.Ensure(path); err != nil {
					logger.Logger.Warn().Err(err).Msg("Failed to apply disable_seeding to the torrent session; the next pass retries")
				}
			}
		}

		response := map[string]any{"message": "Configuration updated successfully"}
		// The torrent client is picked once, at startup (cmd/daemon newTorrentBackend).
		if previous != nil && clientOf(previous) != clientOf(&config) {
			response["restart_required"] = true
		}
		JSONSuccess(w, http.StatusOK, response)
	}
}

func clientOf(c *files.Config) string {
	if c.UsesQBittorrent() {
		return files.TorrentClientQBittorrent
	}
	return files.TorrentClientBuiltin
}

// validateTorrentClient checks torrent_client and the qBittorrent settings. "" when valid.
func validateTorrentClient(c *files.Config) string {
	switch c.TorrentClient {
	case "", files.TorrentClientBuiltin, files.TorrentClientQBittorrent:
	default:
		return `Torrent client must be "builtin" or "qbittorrent"`
	}
	if msg := validatePathMap(c.QBittorrentPathMap); msg != "" {
		return msg
	}
	if !c.UsesQBittorrent() {
		return ""
	}
	if !c.IsDirectMode() {
		return `qBittorrent writes straight into the library: set library_mode to "direct"`
	}
	// 0 would be unlimited in qBittorrent (decisions/144). Only while disable_seeding is on, the
	// one time the value is used and the settings page shows it.
	if c.DisableSeeding && c.QBittorrentDisableSeedingUploadKiB < 1 {
		return "The upload limit for disable seeding with qBittorrent must be at least 1 KiB/s"
	}
	u, err := url.Parse(strings.TrimSpace(c.QBittorrentURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "qBittorrent URL must be an http(s) address, like http://127.0.0.1:8080"
	}
	if len(c.QBittorrentPathMap) > 0 && !underAnyMapping(c.CompletedAnimePath, c.QBittorrentPathMap) {
		return "The library folder is not under any app folder of the qBittorrent path map"
	}
	// qBittorrent downloads staged torrents there itself (decisions/145).
	if staging := c.StagingPath(); staging != "" && len(c.QBittorrentPathMap) > 0 && !underAnyMapping(staging, c.QBittorrentPathMap) {
		return "The incomplete download folder is not under any app folder of the qBittorrent path map"
	}
	return ""
}

// validatePathMap: both sides absolute, and no folder listed twice or inside another on the same
// side — the translation takes the longest prefix, and an overlap would make it ambiguous.
func validatePathMap(maps []files.PathMapping) string {
	for i, m := range maps {
		if !filepath.IsAbs(m.App) || !isAbsAnyOS(m.QBittorrent) {
			return "Every qBittorrent path map entry needs two absolute folders (app and qbittorrent)"
		}
		for _, other := range maps[:i] {
			if nestedOrSame(m.App, other.App) || nestedOrSame(m.QBittorrent, other.QBittorrent) {
				return "The qBittorrent path map lists the same folder twice, or one inside another"
			}
		}
	}
	return ""
}

// isAbsAnyOS accepts an absolute path of either OS: qBittorrent may run on another machine.
func isAbsAnyOS(p string) bool {
	return strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\\`) ||
		(len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/'))
}

func nestedOrSame(a, b string) bool {
	a, b = strings.TrimRight(a, `/\`), strings.TrimRight(b, `/\`)
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/") ||
		strings.HasPrefix(a, b+`\`) || strings.HasPrefix(b, a+`\`)
}

func underAnyMapping(path string, maps []files.PathMapping) bool {
	for _, m := range maps {
		if nestedOrSame(path, m.App) && len(strings.TrimRight(path, `/\`)) >= len(strings.TrimRight(m.App, `/\`)) {
			return true
		}
	}
	return false
}
