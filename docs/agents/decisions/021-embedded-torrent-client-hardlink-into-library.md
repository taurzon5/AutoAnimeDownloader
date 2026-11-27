# 21. Embedded torrent client + hardlink-into-library model (replaces qBittorrent)

**Location:** `internal/torrents/` (`TorrentBackend`, `Session`/`SessionManager`, `FakeBackend`); `internal/files/librarian.go`; `internal/daemon/jobs.go` (`JobOrganize`), `episodes.go`, `verification.go`.

**What it looks like:** The app embeds a full BitTorrent client (`github.com/cenkalti/rain/v2`) and hardlinks completed files into a second directory, instead of the more familiar "talk to a running qBittorrent over its WebUI and let it manage files."

**Why it's right:** Embedding makes the daemon a single self-contained binary — no external qBittorrent to install, run, secure, or keep reachable, and no `qbittorrent_url`/`QBITTORRENT_URL` to configure. Torrents download to `save_path` and keep **seeding** there; on completion the video files are **hardlinked** into `completed_anime_path` (the Jellyfin library). Hardlinking (not copy/move) means:
- No wasted space — the library name and the seeded file share the same bytes.
- Seeding is never interrupted — the seeded file is never moved or renamed (renaming would break the torrent). The Jellyfin name (`"Anime Name - E05.mkv"`, for single episodes and for batch files whose number is readable) lives only on the library hardlink.
- The hard constraint: `save_path` and `completed_anime_path` **must be on the same filesystem/volume** (hardlinks can't cross devices). `completed_anime_path` is therefore now **required**, and the config-save endpoint validated the pair with a real hardlink probe (`Librarian.ProbePaths`), rejecting cross-device paths with HTTP 400. A two-way probe **no longer exists** — today it is `Librarian.ProbePath`, one argument; see the amendment at the end of this entry.

Torrent logic sits behind the `TorrentBackend` interface (`Add`/`List`/`Get`/`Remove`/`Ensure`/`SetCallbacks`/`Close`) so the daemon injects one uniform backend — rain-backed `SessionManager` in production, in-memory `FakeBackend` in tests (the qBittorrent mock server is gone). Resume data lives in a bbolt DB at `~/.autoAnimeDownloader/session.db`, deliberately **outside** `save_path` so it survives a `save_path` change. rain listens on a default port range (20000–30000) with no UPnP/NAT-PMP; inbound peers may need manual forwarding, but DHT+PEX work without it.

**Amendment (see [#136](136-direct-mode-places-a-torrent-before-the-add-and.md), [#137](137-direct-mode-deletes-through-the-torrent-and-keep.md)):** the hardlink-into-library model is
`library_mode: "hardlink"`, the default until 2026-11-26; direct is the default since
([#159](159-direct-is-the-default-library-mode-and-an-empty.md)), and hardlink still applies to configs that saved it and to torrents
added in it. With `"direct"`, rain writes each torrent through a custom storage
straight to its library path and seeds from there: no second directory, no hardlink. Everything
above still holds for every torrent added in hardlink mode, whatever the mode is later.

**Don't "fix" by:** re-introducing an external torrent client or `qbittorrent_url` as the only way (qBittorrent is an option now, see the amendment below); renaming the seeded file to the Jellyfin name (breaks seeding); copying/moving into the library instead of hardlinking (wastes space, stops seeding); or moving `session.db` into `save_path` (loses resume data on a path change). Note the webhook event key string is still `download_completed` — the Go constant was renamed `QBittorrentDownloadCompleted → DownloadCompleted`, but that is not user-visible; don't "fix" a webhook that isn't broken.

**Amendment (see #31):** `save_path` is no longer a user-configured field — the download directory is now derived from `completed_anime_path` (`Config.DownloadPath`). What was previously a constraint on the user's configuration ("these two fields must be on the same filesystem, and the endpoint validates it") is now an architectural invariant that holds by construction: there is only one path, so there is nothing to be on a different filesystem from. `Librarian.ProbePaths` (two arguments) is gone; the surviving probe is `Librarian.ProbePath` (one argument, #26).

**Amendment (see [#142](142-qbittorrent-stops-each-placed-torrent-at-its.md), [#143](143-the-qbittorrent-password-and-api-key-come-back.md)):** qBittorrent is back as an
option, `torrent_client: "qbittorrent"`, behind the same `TorrentBackend` (`QBitBackend`), in direct
mode only: it writes the library in place, so the hardlink model above doesn't apply to it. The
embedded client stays the default, and `qbittorrent_url` returned with it. So did a
`QBITTORRENT_URL` variable, but only for `docker/entrypoint.sh`, which writes it into the first
`config.json` (the compose file's `qbittorrent` profile). The old runtime override of the URL did
not come back. The user has since
decided to drop rain for qBittorrent
([megapack spec](../../superpowers/specs/2026-11-23-megapack-downloads-design.md)); when that
lands, this entry is reversed, not amended again.
