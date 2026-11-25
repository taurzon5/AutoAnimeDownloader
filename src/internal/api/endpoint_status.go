package api

import (
	"AutoAnimeDownloader/src/internal/anilist"
	"AutoAnimeDownloader/src/internal/daemon"
	"AutoAnimeDownloader/src/internal/diagnostics"
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/torrents"
	"AutoAnimeDownloader/src/internal/updates"
	"AutoAnimeDownloader/src/internal/version"
	"net/http"
	"time"
)

type StatusResponse struct {
	Status    string    `json:"status" example:"running"`
	LastCheck time.Time `json:"last_check" example:"2024-01-01T00:00:00Z"`
	HasError  bool      `json:"has_error" example:"false"`
	Version   string    `json:"version" example:"1.2.0"`
	DiskTotal uint64    `json:"disk_total" example:"500107862016"`
	DiskFree  uint64    `json:"disk_free" example:"128849018880"`
	// DiskLow marks that the daemon would refuse a new torrent of size 0: free minus what the
	// session still has to write, against min_free_disk_gb. Computed on the server on purpose: a
	// threshold duplicated in the frontend would end up disagreeing with what the daemon is doing.
	DiskLow bool `json:"disk_low" example:"false"`
	// Disks is one entry per download root (decisions/154): its name, path and volume. Low marks a
	// root that can't take a new torrent now. With one root it is the library, the same volume as
	// disk_total/disk_free.
	Disks []StatusDisk `json:"disks,omitempty"`
	// IncompleteDiskTotal and IncompleteDiskFree describe the incomplete_path volume, only when
	// that folder is set and on another filesystem than the library (decisions/141). disk_low covers
	// both volumes.
	IncompleteDiskTotal uint64 `json:"incomplete_disk_total,omitempty" example:"1000204886016"`
	IncompleteDiskFree  uint64 `json:"incomplete_disk_free,omitempty" example:"266287972352"`
	// Anilist is AniList's last known state, written by ANY call (the daemon's pass or the
	// frontend's poll). It travels here, and not in its own endpoint, because /status is already
	// polled and is already the payload the WebSocket pushes — see decisions/066.
	Anilist anilist.Health `json:"anilist"`
	// PendingCrashAt is when the daemon last crashed, while nobody has handled the report yet; null
	// otherwise. The UI opens the report screen (fresh page load) or toasts (tab already open).
	PendingCrashAt *time.Time `json:"pending_crash_at"`
	// LatestVersion / LatestVersionURL are the newer release the update check found and its page;
	// both "" when there is none, the check is off (check_for_updates), it hasn't run yet, or the
	// build is "dev". Here because /status is what the shell already polls for the version.
	LatestVersion    string `json:"latest_version" example:"2.11.0"`
	LatestVersionURL string `json:"latest_version_url" example:"https://github.com/icarosuper/AutoAnimeDownloader/releases/tag/v2.11.0"`
}

// StatusDisk is one download root's volume in GET /status.
type StatusDisk struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
	Low   bool   `json:"low"`
}

// handleStatus returns the current status of the daemon, including last check time, error state,
// and disk space for the filesystem of completed_anime_path (the library)
func handleStatus(server *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			JSONError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET method is allowed")
			return
		}

		status, lastCheck, hasError := server.State.GetAll()

		var diskTotal, diskFree, incompleteTotal, incompleteFree uint64
		diskLow := false
		var disks []StatusDisk
		// The library volume holds the download directory by construction (Config.DownloadPath); the
		// incomplete folder, when set on another drive, is reported on its own (decisions/141).
		if cfg, err := server.FileManager.LoadConfigs(); err == nil && cfg.CompletedAnimePath != "" {
			diskTotal, diskFree, _ = files.DiskSpace(cfg.CompletedAnimePath)
			var session []torrents.TorrentInfo
			if server.Torrents != nil {
				session = server.Torrents.List()
			}
			var saved []files.EpisodeStruct
			if eps, err := server.FileManager.LoadSavedEpisodes(); err == nil {
				saved = eps
			}
			diskLow = daemon.DiskLow(cfg, server.Torrents, session, saved)
			for _, d := range daemon.RootDisks(cfg, server.Torrents, session, saved) {
				disks = append(disks, StatusDisk{Name: d.Name, Path: d.Path, Total: d.Total, Free: d.Free, Low: d.Low})
			}
			if staging := cfg.StagingPath(); staging != "" && !files.SameFilesystem(staging, cfg.CompletedAnimePath) {
				incompleteTotal, incompleteFree, _ = files.DiskSpace(staging)
			}
		}

		response := StatusResponse{
			Status:    string(status),
			LastCheck: lastCheck,
			HasError:  hasError,
			Version:   version.Version,
			DiskTotal: diskTotal,
			DiskFree:  diskFree,
			DiskLow:   diskLow,
			Disks:     disks,
			Anilist:   anilist.CurrentHealth(),

			IncompleteDiskTotal: incompleteTotal,
			IncompleteDiskFree:  incompleteFree,
		}
		response.LatestVersion, response.LatestVersionURL = updates.Latest()
		if server.DataDir != "" {
			response.PendingCrashAt = diagnostics.PendingCrashAt(server.DataDir)
		}

		JSONSuccess(w, http.StatusOK, response)
	}
}
