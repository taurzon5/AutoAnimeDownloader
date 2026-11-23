# qBittorrent backend: an external client instead of the built-in one

**Date:** 2026-11-23
**Status:** designed, not implemented — no fixed release
**Origin:** the user wants to run a qBittorrent fork they will modify themselves (base not chosen:
stock or Enhanced Edition) in place of the built-in rain client, with direct library mode, and with
qBittorrent's own queueing and seeding limits. Per-file selection inside packs is deferred until the
user has planned it.
**Scope:** `internal/torrents` (a `qbittorrent` backend behind `TorrentBackend`), `internal/files`
(config, path mapping), `internal/daemon` (backend choice, the direct-mode placement path),
`internal/diagnostics` (secret redaction), `internal/api` (validation, a connection test),
`frontend` (settings group), CLI (`config set` covers the fields). Builds on
[direct library mode](2026-11-22-direct-library-mode-design.md) PR 1, which is on `master`.

---

## The problem

rain is embedded and self-contained, but it can't do some things qBittorrent does: anti-leech
policies (Enhanced Edition), per-file priorities, mature queueing and share limits. And it isn't
code the user wants to change. The user wants the daemon to drive a qBittorrent build they control.

## What this is not

- **Not a return of the old integration.** The pre-`cd496c3` qBittorrent client (420 lines) predates
  the queue, pause/resume/prioritize, per-file progress, completion events and placements. It is a
  reference, not a base.
- **Not hardlink mode.** With qBittorrent the library is always written in place (direct mode).
  There is no `.torrents` download copy and no hardlink.
- **Not "API only".** The app must be able to see the files qBittorrent writes — on the same machine,
  in Docker, or across machines through a shared filesystem — because organize, the nfo, the disk
  guard and the leftover cleanup read and write the library. Paths may differ on each side.
- **Not per-file selection.** File priorities stay at "download everything" until the user's own
  design for packs lands.
- **Not a migration tool, in v1.** See [Switching clients](#switching-clients).

---

## Settings

| JSON key | Type | Default | Meaning |
|---|---|---|---|
| `torrent_client` | `string` | `"builtin"` | `"builtin"` = rain, as today. `"qbittorrent"` = this spec |
| `qbittorrent_url` | `string` | `""` | WebUI base URL, e.g. `http://127.0.0.1:8080`. Required with `"qbittorrent"`. (The key existed before and was removed with the old integration; old configs never carry it any more) |
| `qbittorrent_username` / `qbittorrent_password` | `string` | `""` | WebUI login. The password is a **secret**: never logged, redacted in the bug report bundle, sent back by `GET /config` as a placeholder |
| `qbittorrent_category` | `string` | `"autoanimedownloader"` | Category every torrent of the app gets. The backend only lists, controls and deletes torrents in it, so a qBittorrent shared with other uses is left alone |
| `qbittorrent_path_map` | `[]PathMap` | `[]` | `{"app": "/mnt/nvme01/Animes", "qbittorrent": "/downloads"}`: the same folder as each side sees it. Empty = identical paths (same machine) |

`library_mode` must be `"direct"` with `"qbittorrent"` (validated). `completed_anime_path` stays the
library root **as the app sees it**; every path sent to qBittorrent goes through the map, and every
path read back comes back through it.

Validation (`handleUpdateConfig`): the URL parses; the map has no duplicate or nested entries on
either side; `completed_anime_path` (and later every download root) falls under an `app` entry
when the map is not empty. A `POST /api/v1/torrent-client/test` button (and `aad torrents test`,
CLI parity) logs in, reads the Web API version and checks that a probe file the app writes under
the library is visible to qBittorrent (`app/getDirectoryContent` on the mapped folder).

---

## Requirements on qBittorrent

- **Web API 2.11 or later (qBittorrent 5.0+).** 2.11 renamed `torrents/pause|resume` to
  `torrents/stop|start`; supporting both is cheap (pick by version at login) but 4.x is end of life.
  Open question 1.
- **Both sides see the library.** Same machine, a bind mount in Docker, or the same NFS/SMB share
  mounted on both hosts. The path map translates; it cannot make an unshared disk visible.
- **Preferences the app relies on** are read at connect and reported, never silently overwritten:
  - "Append .!qB extension to incomplete files" ↔ `incomplete_files: "part"` (the app accepts `.!qB`
    instead of `.part`; Jellyfin skips both);
  - automatic torrent management **off** for the app's category (the app sets each torrent's save
    path itself).
  Open question 2: report-only or let the app set them.

---

## The backend

`internal/torrents/qbittorrent.go`: a `QBitBackend` implementing `TorrentBackend`, selected in
`cmd/daemon/main.go` by `torrent_client`. The fake backend and every daemon test stay as they are.

| Interface method | qBittorrent |
|---|---|
| `Ensure` | log in (`auth/login`, SID cookie), read `app/webapiVersion`, create the category if missing; re-login on 403. Returns `created=true` on the first successful connect so the pass reconciles |
| `Add` | `torrents/add` with `urls=<magnet>`, `category`, `savepath` (mapped, from the placement), `contentLayout`, `autoTMM=false`; the hash comes from the magnet, as today |
| `List` / `Get` | `sync/maindata` (incremental `rid`) filtered to the category; fields map onto `TorrentInfo` (`content_path` → `DataDir` through the map, `progress`/`size`/`downloaded` → bytes, `dlspeed`/`upspeed`, `num_seeds`+`num_leechs`, `eta`, `added_on`, `state` → the app's status slugs) |
| `Files` | `torrents/files` (`name`, `size`, `progress`) |
| `Remove` | `torrents/delete` with `deleteFiles=!keepData` |
| `Pause` / `Resume` / `Prioritize(All)` | `torrents/stop` / `start` / `topPrio` (in order, for the batch) |
| `SetMaxActiveDownloads` | qBittorrent's queue (`queueing_enabled`, `max_active_downloads`) — see [Queue and limits](#queue-and-limits) |
| `SetUploadDisabled` | per-torrent upload limit and a share limit of ratio 0 (open question 3: qBittorrent has no "upload nothing at all") |
| `Announce` | `torrents/reannounce` |
| `SetCallbacks` | a poll loop on `sync/maindata` (every few seconds): a torrent crossing into a completed state fires `onComplete`; `error`/`missingFiles` fires `onFailed`. A completed torrent seen for the first time after a restart is left to the reconcile pass, as with rain |
| `SetDirectMode` | no-op: always direct |
| `SetPlacement` / `Placement(s)` / `FinalizePlacement` / `ForgetPlacement` / `KeepPlacement` | the same `placementStore` (`placements.json`); see below |
| `ConsumeRootSwap` | the `.aad_root` marker moves to the library root (there is no `.torrents`); same dual-marker rule |
| `Close` | stop the poll loop, `auth/logout` |

**Status slugs.** `downloading`/`forcedDL`/`metaDL` → downloading (`metaDL` → downloading_metadata);
`stalledDL` → downloading; `queuedDL` → queued; `stoppedDL`/`pausedDL` → stopped;
`checkingDL`/`checkingUP`/`checkingResumeData` → verifying; `allocating` → allocating;
`uploading`/`stalledUP`/`forcedUP`/`queuedUP` → seeding; `stoppedUP`/`pausedUP` → stopped (completed);
`moving` → verifying; `error`/`missingFiles` → stopped plus `onFailed`. Completed is
`progress == 1`, never read from the state (the same reason as `completedFromStats`).

## Placement through qBittorrent

The placement logic of direct mode stays in the app (`daemon/placement.go`, `planFiles`): one rule
for both clients. Only how it is applied changes.

- **Before the add**, the placement is registered as today. `savepath` is the mapped series folder
  for a pack (`contentLayout=Original`: the pack keeps its own folders), the mapped
  `Season NN/` folder for an episode and `Specials/` for a movie (`contentLayout=NoSubfolder`: files
  land directly in it).
- **When the metadata arrives** (the poll loop sees the file list), the backend runs `planFiles`
  over `torrents/files` and freezes the map exactly as rain's `Prepare` does. Every file whose
  planned name differs from qBittorrent's (a Jellyfin rename, an occupied destination's
  `[hash8]` suffix, the flattened folder of an episode torrent) is renamed with
  `torrents/renameFile` before the download gets far: renaming an incomplete file in qBittorrent
  is supported.
- **Finalize** is qBittorrent's own: with "Append .!qB" on, it drops the extension at completion.
  `FinalizePlacement` only waits for the final names to exist and marks the placement completed.
- **Deletion** is `torrents/delete` with `deleteFiles`; the app then prunes the folders left empty
  with the same rule (`torrents.RemovePlacedFiles` minus the file deletes), since qBittorrent
  leaves empty folders behind.
- **Keep forever / auto delete / Kept / the orphan sweep** work unchanged: they read the
  placement store and the records, not the client.

## Queue and limits

With `"qbittorrent"`, qBittorrent's queue replaces the app's:

- `max_concurrent_downloads` sets qBittorrent's `max_active_downloads` (queueing on) **for the
  whole client**. Open question 2 again: a shared qBittorrent may not want that.
- Prioritize (web UI, CLI, manual downloads) is `topPrio`; resume is `start` and lets qBittorrent's
  queue decide; pause is `stop`. The app's `queue.go` is not used by this backend.
- Seeding: keep forever = no share limit on the app's torrents (`ratioLimit=-1`,
  `seedingTimeLimit=-1`). A seeding policy (ratio, days) becomes possible here as a later option.

## Switching clients

v1: `torrent_client` is fixed per installation in practice. Switching it does not move torrents.
A record whose hash the new client doesn't have reads as missing and is downloaded again, so
switching re-downloads everything still tracked. The settings page says so next to the switch.

The real fix is an import: re-add each tracked direct-mode torrent to the other client with its
save path at the files' location, letting the client verify instead of download. That is a
separate PR (open question 4).

---

## Decisions this touches

[decisions/021](../../agents/decisions/021-embedded-torrent-client-hardlink-into-library.md)
(embedded client replaces qBittorrent) gets an amendment: the embedded client stays the default;
qBittorrent comes back as an option, in direct mode only, behind the same interface.
[decisions/041](../../agents/decisions/041-the-download-queue-holds-every-incomplete-torrent.md)
(the app's queue) is scoped to the built-in client. The status-slug mapping must stay in sync with
`src/lib/utils/torrentStatus.ts`, as for rain. New entries when implemented: completion by polling
`sync/maindata`; the category as the boundary of what the app touches; qBittorrent applies the
app's placement through renames.

## Delivery

1. **The backend:** config, path map, login, list/get/files, add with save path, delete, pause/
   resume/prioritize, the poll loop with callbacks, the connection test (API + CLI), status slugs,
   redaction of the password. Direct mode only; placement renames.
2. **Queue and limits:** qBittorrent's queue for `max_concurrent_downloads`, share limits for
   keep forever, `disable_seeding`.
3. **Import between clients** (if open question 4 says yes).

Per-file selection is not on this list: it waits for the user's design.

## Tests

- A mock qBittorrent Web API server (`src/tests/mocks/qbittorrent`, revived and rewritten for
  API 2.11+): login and SID, a 403 forcing re-login, `sync/maindata` incremental updates,
  `torrents/add` honoring savepath/category/contentLayout, `renameFile`, `delete` with and
  without files.
- Backend unit tests against the mock: every interface method; the state → slug table; completion
  and failure callbacks fired once; path mapping both ways; only the app's category is touched.
- Placement through renames: an episode renamed to `Series - S01E05.mkv`, a collision suffix,
  a pack kept as-is.
- `sandbox`: a qBittorrent mock wired in place of the built-in client.
- Optional, not in CI: an integration test against a real qBittorrent-nox in Docker.

## Open questions

1. **Minimum version:** 5.0+ only (simplest), or also 4.x (`pause`/`resume`)?
2. **Global qBittorrent preferences** (incomplete-file extension, auto torrent management, the
   active-download cap): report and ask the user to set them, or let the app set them — which
   affects every torrent in a qBittorrent shared with other uses?
3. **`disable_seeding` with qBittorrent:** qBittorrent can't refuse all uploads (an upload limit of
   0 means unlimited). Use the smallest limit plus ratio 0, or disable the option for this client?
4. **Switching clients:** accept the re-download in v1, or make the import part of the first
   delivery?
5. **Auth:** username/password only, or also qBittorrent's API keys (Web API 2.14.1+)?
