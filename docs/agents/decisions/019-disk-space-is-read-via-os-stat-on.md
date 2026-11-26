# 19. Disk space is read via OS stat on `CompletedAnimePath`

**Location:** `internal/files/diskspace_unix.go`, `internal/files/diskspace_windows.go`; `internal/api/endpoint_status.go` (`handleStatus`).

**What it looks like:** Reading disk space with a raw platform-specific filesystem syscall on a local path, when a portable Go library might seem cleaner.

**Why it's right:** The dashboard needs both total capacity **and** free space — the disk card shows both; the syscall pair (`Statfs` / `GetDiskFreeSpaceEx`) is the direct way to get both. `handleStatus` swallows stat errors (empty/unreadable `CompletedAnimePath`) rather than surfacing them, so a bad path just hides the disk card instead of breaking `/api/v1/status`.

> **Revision (embedded-client refactor):** The original rationale compared this against qBittorrent's `free_space_on_disk` API (which only reported free space, not total). That comparison is now moot — the torrent client is embedded and there is no qBittorrent API to read from. The old cross-host mount-mismatch caveat no longer applies either. The OS-stat approach stands as the only and correct source.

> **Amendment (see #31):** disk space is now measured on `CompletedAnimePath`, not the legacy `save_path` field. Since the download directory is derived from `CompletedAnimePath` (`Config.DownloadPath`), the two are always on the same filesystem by construction, so measuring the library path also correctly reports free space for downloads-in-progress.

**Amendment (see [#141](141-staged-downloads-are-copied-into-the-library-while.md)):** with `incomplete_path` on another drive, downloads are written there
first, so that drive is stat'ed too (`checkRootBudgetSpace`, `GET /status` `incomplete_disk_*`, the
`disk_low` alert's `diskAlertVolume`). `CompletedAnimePath` still covers everything else.

**Don't "fix" by:** trying to route disk stats through the torrent library — it has no such API, and `CompletedAnimePath` is on the same filesystem as the download directory the daemon writes to.

**Amendment (see [#154](154-several-download-roots-each-placement-records-its.md)):** with several download roots, each root's volume is stat'ed for its own disk guard and its line in `GET /status`; the `disk_low` alert (`diskAlertVolume`) watches the root with the most room above its own floor (`roomiestRoot`, 2026-11-26), and the library's volume only when no download roots are listed.
