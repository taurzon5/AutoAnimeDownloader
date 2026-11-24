# qBittorrent backend: an external client instead of the built-in one

**Date:** 2026-11-23
**Status:** step 1 (the backend) implemented on branch `feat/qbittorrent-backend`, not merged;
steps 2–3 not started — no fixed release. **Since 2026-11-23 rain is being
dropped** ([megapack-downloads](2026-11-23-megapack-downloads-design.md)), so this backend becomes
the app's only client (see "Since this was written"); the open questions are answered at the end.
**Origin:** the user wants to run a qBittorrent fork they will modify themselves (base not chosen:
stock or Enhanced Edition) in place of the built-in rain client, with direct library mode, and with
qBittorrent's own queueing and seeding limits. Per-file selection inside packs was deferred until
the user had planned it — the megapack spec now uses qBittorrent's per-file priorities.
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
- **Not a migration tool, in v1.** An import from rain is designed but not a priority (answer 4).
  See [Moving from rain](#moving-from-rain-the-import).

---

## Since this was written: rain is being dropped

On 2026-11-23 the user decided the built-in client goes and qBittorrent is the only one (the
megapack spec needs per-file selection, which rain lacks). Other clients were weighed the same day
(Deluge, rTorrent, Transmission, Porla, `anacrolix/torrent`); the user stuck with qBittorrent. This
spec was written for "an alternative"; what that changes here:

- **`torrent_client` is temporary.** It exists for the move only: `"builtin"` until an install
  switches, then `"qbittorrent"`; the key goes away with rain.
- **Direct mode is the only library mode for new torrents.** Hardlink mode needs rain's separate
  `.torrents` copy.
- **[#21](../../agents/decisions/021-embedded-torrent-client-hardlink-into-library.md) is reversed,
  not amended**, and [#41](../../agents/decisions/041-the-download-queue-holds-every-incomplete-torrent.md)
  (the app's own queue) goes with rain.
- **More of qBittorrent's API is used** by the other specs: per-file priorities
  (`torrents/filePrio`) for megapacks, and the incomplete folder as qBittorrent's "keep incomplete
  torrents in" (`temp_path`).
- **Docker:** the compose file gets a qBittorrent container next to the app, sharing the library
  folder at the same path, so the path map stays empty.

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
  Answer 1: 5.0 or later only.
- **Both sides see the library.** Same machine, a bind mount in Docker, or the same NFS/SMB share
  mounted on both hosts. The path map translates; it cannot make an unshared disk visible.
- **Preferences the app relies on** are read at connect; the app sets them only when "Let the app
  manage this qBittorrent's settings" is on (answer 2), and otherwise reports what differs:
  - "Append .!qB extension to incomplete files" ↔ `incomplete_files: "part"` (the app accepts `.!qB`
    instead of `.part`; Jellyfin skips both);
  - automatic torrent management **off** for the app's category (the app sets each torrent's save
    path itself).

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
| `SetUploadDisabled` | the smallest per-torrent upload limit while downloading, then the torrent is removed (files kept) once organized — qBittorrent has no "upload nothing at all" (answer 3) |
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
  whole client**, so it is set only when the app manages the settings (answer 2); otherwise the
  settings page shows qBittorrent's own cap next to it.
- Prioritize (web UI, CLI, manual downloads) is `topPrio`; resume is `start` and lets qBittorrent's
  queue decide; pause is `stop`. The app's `queue.go` is not used by this backend.
- Seeding: keep forever = no share limit on the app's torrents (`ratioLimit=-1`,
  `seedingTimeLimit=-1`). A seeding policy (ratio, days) becomes possible here as a later option.

## Moving from rain: the import

> **Not a priority** (answer 4): the first release switches without it, and what rain was tracking
> is downloaded again. This section is kept so the work isn't redone if it is wanted later.

The import would run once, from the settings page (and the CLI), while rain is still there to read
from:

1. **For each torrent the app tracks**, it takes the `.torrent` metadata from rain's session — the
   same bytes the redownload already re-adds
   ([#140](../../agents/decisions/140-redownloading-missing-files-adds-the-same-torrent.md)) — so
   qBittorrent needs no magnet lookup.
2. **It adds it to qBittorrent** in the app's category with the save path where the files already
   are, checking on, stopped until the check ends: qBittorrent verifies the files and seeds them
   instead of downloading. Direct-mode torrents point at their library folders; their file names
   already match the placement.
3. **Hardlink-era torrents keep their data where it is:** qBittorrent seeds them from
   `.torrents/<hash>/`, and the library hardlinks stay. They are handled like direct-mode torrents
   from then on, and the `.torrents` folder empties as they are deleted or upgraded.
4. **A torrent whose files fail the check** (deleted, partly missing) is reported on the import
   page with the choice to download it again or forget it.
5. When every torrent is in qBittorrent, `torrent_client` becomes `"qbittorrent"` and rain's
   session is closed.

The check reads every file once, sequentially. On a large library on a slow disk it takes hours;
the import page shows progress, and the app keeps working with rain until the switch.

---

## Decisions this touches

[decisions/021](../../agents/decisions/021-embedded-torrent-client-hardlink-into-library.md)
(embedded client replaces qBittorrent) is **reversed** now that rain is dropped: qBittorrent is the
client, in direct mode, behind the same interface. (Written before that decision, this said "an
amendment: the embedded client stays the default".)
[decisions/041](../../agents/decisions/041-the-download-queue-holds-every-incomplete-torrent.md)
(the app's queue) goes with rain; qBittorrent's queue replaces it. The status-slug mapping must stay in sync with
`src/lib/utils/torrentStatus.ts`, as for rain. New entries when implemented: completion by polling
`sync/maindata`; the category as the boundary of what the app touches; qBittorrent applies the
app's placement through renames.

## Delivery

1. **The backend:** config, path map, login, list/get/files, add with save path, delete, pause/
   resume/prioritize, the poll loop with callbacks, the connection test (API + CLI), status slugs,
   redaction of the password. Direct mode only; placement renames.
2. **Queue and limits:** qBittorrent's queue for `max_concurrent_downloads`, share limits for
   keep forever, `disable_seeding`.
3. **Import from rain** — not scheduled; only if it is wanted later (answer 4).

Per-file selection comes after these, with the megapack spec (`torrents/filePrio`).

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

## Open questions — answered (2026-11-23)

The user asked for these to be answered, after choosing to stick with qBittorrent. Say if you
disagree with any.

1. **Minimum version: qBittorrent 5.0 or later (Web API 2.11+), no 4.x.** 4.x is end of life; 5.0
   renamed `pause`/`resume` to `stop`/`start`, so one code path instead of two. 5.0.1 added the
   "Simple pread/pwrite" disk mode, which avoids libtorrent 2.0's memory-mapped I/O — what the
   user's low-memory, SMR-disk setup wants. Debian trixie ships 5.1.0, and the user's fork will
   start from current code anyway.
2. **Global preferences: a switch, "Let the app manage this qBittorrent's settings".** On, the app
   sets what it needs at connect; off, it only reports what differs and the settings page says
   what to change. The switch is **on in the app's own Docker setup** (a qBittorrent that only the
   app uses) and **off by default otherwise** (a qBittorrent shared with other uses). Everything that
   can be set per torrent is set per torrent either way: save path, automatic torrent management
   off, share limits, upload limit. What the switch governs is the truly global part: the
   incomplete-file extension, the incomplete folder (`temp_path`), the active-download cap and the
   disk I/O mode (pread recommended).
3. **`disable_seeding`: keep it, as "almost no upload".** While downloading, each torrent gets the
   smallest upload limit qBittorrent accepts (0 means unlimited there, so not 0); as soon as an
   episode is in the library, the app removes the torrent and keeps the files — in direct mode the
   files are the library. The setting's hint says "almost no upload" instead of "no upload at
   all", and [#119](../../agents/decisions/119-disable-seeding-means-no-upload-at-all-and-a.md)
   gets amended.
4. **Switching: not a priority** (the user, 2026-11-23: "switching from rain spec is not really
   important, don't prioritise it"). The first release switches without an import: what rain was
   tracking reads as missing in qBittorrent and is downloaded again, and the settings page says so
   before the switch. "Moving from rain" stays as a design for later, not scheduled.
5. **Sign-in: an API key when qBittorrent offers one, otherwise user and password.** With Web API
   2.14.1 or later, paste a key created in qBittorrent's WebUI. Older 5.x has no keys, so the user
   and password are stored as a secret (never logged, redacted in bug reports, not echoed back).
   The settings page says which one is in use.
