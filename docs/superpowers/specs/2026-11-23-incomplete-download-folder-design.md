# Incomplete download folder: download on a fast drive, move into the library when finished

**Date:** 2026-11-23
**Status:** Designed, not implemented — the open questions at the end need the user
**Origin:** the user's library drive is a 4 TB Seagate USB hard disk, very likely SMR (shingled).
Downloading writes pieces in random order. On an SMR drive that fills the drive's small fast cache,
and after that every write rewrites a whole band: a few MB/s, with stalls of several seconds.
Reading (seeding) and long sequential writes are fine. Every torrent client's answer is the same
("Keep incomplete torrents in" in qBittorrent): download on a fast drive, move the finished files.
**Scope:** `internal/torrents` (placement, storage, finalize becomes a move), `internal/files`
(config field, validation), `internal/daemon` (disk guard on two volumes, organize, orphan sweep,
health), `internal/api` (config validation, status), frontend (setting, a "moving" state in
Downloads, i18n). No new endpoint and no new CLI command: `config set incomplete_path …`.

---

## The problem

Direct mode writes each torrent straight into the library, as `.part` files that are renamed when
the torrent finishes ([decisions/136](../../agents/decisions/136-direct-mode-places-a-torrent-before-the-add-and.md)).
Hardlink mode downloads into `<library>/.torrents`, which hardlinks force onto the library's
filesystem. Either way, a library on an SMR drive takes every download as random writes.

In direct mode the library file is also the seeding file, so the data must end up on the library
drive. It only has to get there once, as one sequential write.

## What this is not

- **Not for hardlink mode** ([open question 1](#open-questions)). There the seeding copy lives in
  `.torrents`, on the library's filesystem by construction; staging would add a second move.
  Hardlink-mode torrents ignore the setting.
- **Not a mover for existing torrents.** Like every direct-mode choice, the staging folder is
  frozen into the placement when the torrent is added. Turning the setting on, changing it or
  clearing it only affects torrents added afterwards.
- **Not per root.** One staging folder serves every root of the direct-mode spec's PR 2 and
  `movies_root`.

---

## Setting

| JSON key | Type | Default | Meaning |
|---|---|---|---|
| `incomplete_path` | `string` | `""` | Direct mode only. Absolute path of an existing folder on a fast drive. Empty = off, today's behavior |

Validated at config save, next to the library checks:

- **Absolute, and it must already exist.** The app never creates this folder. If the drive is not
  mounted, its mount point is an empty directory on the system disk, and creating the folder there
  would quietly fill the system disk.
- **Writable:** a probe file, without `MkdirAll` (unlike `files.ProbeWritable`).
- **Not inside the library, and the library not inside it.** Jellyfin would scan partial files,
  and the move would be pointless.
- **Hardlink mode:** ignored, and hidden in the settings page.

The same filesystem as the library is allowed (the move is then a rename), but it doesn't help an
SMR drive; the setting's hint says so.

## Layout

```
<incomplete_path>/<infohash>/<library-relative path>[.part]

/mnt/nvme01/aad-incomplete/1c402dde…b7a2/Frieren/Season 01/Frieren - S01E05.mkv.part
```

- **One folder per infohash:** two torrents can't collide, and cleanup is one folder.
- **Nothing new to store:** the path derives from the frozen file map.
- **`.part` follows `incomplete_files`,** as in the library today
  ([open question 2](#open-questions)).

## Placement

`PlacementRequest` gains `IncompleteRoot string` (`json:"incomplete_root,omitempty"`). It is
absolute, copied from `incomplete_path` when the placement is registered, and frozen with the rest
of the placement. It is not stored relative like the library paths: it is outside the library, and
"the user moved the library and re-pointed the config"
([decisions/034](../../agents/decisions/034-a-download-folder-swap-is-detected-by-a-dual.md)) does
not apply to it.

**Where a file is while its placement is not `Completed`** (`placedStorage.Open`), first match wins:

1. **Its final library path**, if a file of the torrent file's size is there. Two cases: a crash
   between the move's rename and its save, and a redownload that reuses files still in the library
   ([decisions/140](../../agents/decisions/140-redownloading-missing-files-adds-the-same-torrent.md)).
2. **The staging path** `<IncompleteRoot>/<hash>/<path>[.part]`, when `IncompleteRoot` is set.
3. **Today's rule:** the `.part` name or the final name in the library.

This one rule replaces today's crash fallback in `Open`, which is case 1 for `.part` files only.

`Completed` placements don't change: the final library path, and a missing file is refused
([decisions/139](../../agents/decisions/139-a-direct-mode-file-deleted-outside-the-app-is.md)).

**The staging root is unavailable** (drive unmounted, folder deleted):
- `Open` never creates `IncompleteRoot` itself, only the folders below it. A missing root returns
  `ErrIncompleteFolderUnavailable`, and rain stops the torrent with that error.
- `HandleTorrentFailure` leaves such a torrent alone, as it does for 139. Dropping and re-adding it
  would only fail again in a loop.
- The [per-pass check](#health) resumes those torrents once the folder is back.

---

## Finishing: copy, switch, clean up

Today `FinalizePlacement` stops the torrent, renames `.part` → final, sets `Completed` and starts
the torrent again. With `IncompleteRoot` set it becomes three phases, so that the slow part blocks
nothing:

1. **Copy, while seeding goes on.** Each file not already at its final path is copied from staging
   to `<final>.aad-moving` in the library: a large buffer, sequential, `fsync` at the end.
   - **Seeding continues:** the torrent keeps seeding from the staging copy, because a finished
     torrent only reads.
   - **No session lock:** today `FinalizePlacement` runs under `SessionManager`'s read lock. A copy
     of several minutes there would let a waiting writer (`Ensure`) block every `List()` call behind
     it, and the web UI would freeze.
2. **Switch, short, under the lock.**
   - Stop the torrent and wait (`stopAndWait`).
   - Rename each `.aad-moving` to its final name.
   - Set `Completed`, save the placements, start the torrent.
   - rain finds every file at its final path, with the stored complete bitfield, and seeds from the
     library at once, with no recheck. Trusting the bitfield is right here, unlike 140's case: a
     final name only ever appears by renaming a fully copied, fsynced file.
3. **Clean up:** remove `<IncompleteRoot>/<hash>/`.

**Same filesystem** (the `st_dev` of the staging root and the library match): phase 1 is skipped,
and phase 2 renames each staging file straight to its final path, today's cost.

**One move at a time.** Organize jobs already run one by one in the job loop (`processDueJobs`).
That is what an SMR drive needs: two copies running at the same time are random writes again. A
mutex around phase 1 keeps it that way for any other caller.

**Shutdown.** The copy watches the job queue's stop channel. `JobQueue.Stop` waits for the loop,
and a 50 GB pack going to a USB disk takes several minutes. A cancelled copy leaves a
`.aad-moving` file behind, and the next organize overwrites it.

**Crash points:**
- **During phase 1:** the placement is not `Completed` and the staging files are intact. After the
  restart, the torrent seeds from staging. Completion or the pass's reconcile enqueues organize
  again, and the copy starts over, overwriting the `.aad-moving` file.
- **During phase 2, after some renames:** `Open` finds those files at their final path (rule 1) and
  the rest in staging. Organize copies only what is not in place yet.
- **After the save, before cleanup:** leftovers stay in staging, and the [sweep](#deletion-failures-orphans)
  removes them.

**The library drive is full at move time:** phase 1 fails, and organize retries with backoff (20
tries), after which the pass's reconcile enqueues it again. Meanwhile the torrent keeps seeding
from staging, and `disk_low` already alerts.

---

## Disk guard

When staging is on another filesystem there are two volumes, and an add must pass both
([decisions/093](../../agents/decisions/093-the-disk-guard-has-two-triggers-and-the-size-one.md),
[/101](../../agents/decisions/101-the-disk-guard-counts-what-the-queue-still-has-to.md)):

- **Staging volume:** the new torrent's size, plus what the staged torrents still downloading have
  left to write.
- **Library volume:** the new torrent's size, plus the full size of every staged torrent not moved
  yet (all of it lands there), plus what torrents writing straight into the library have left.

Rules:
- `min_free_disk_gb` applies to both volumes ([open question 3](#open-questions)).
- `DiskLow` covers both. `GET /status` adds `incomplete_disk_total` and `incomplete_disk_free`, only
  when the setting is on.
- The bulk redownload (140) counts its missing bytes on both volumes.

## Deletion, failures, orphans

- **Deleted before completion** (web UI or CLI, without keep data): `RemovePlacedFiles` also removes
  `<IncompleteRoot>/<hash>/` and any `<final>.aad-moving`
  ([decisions/137](../../agents/decisions/137-direct-mode-deletes-through-the-torrent-and-keep.md)).
  A delete during phase 1 cancels that hash's copy first.
- **Failure drop, keeping data**
  ([decisions/024](../../agents/decisions/024-a-failed-torrent-is-dropped-from-the-session-and.md)):
  the staged files stay with the placement, and the re-add resumes into them.
- **Orphan sweep** (`pruneOrphanPlacements`): also lists the folders inside `incomplete_path` and
  inside each placement's `IncompleteRoot`. It removes a `<hash>` folder whose placement is gone or
  `Completed`, after the same 1 h grace.
  - Kept placements' staging is left alone.
  - Only folders named like an infohash are touched.
- **Redownload missing files** (140): `reopen` sets `IncompleteRoot` to the current
  `incomplete_path`. Files still in the library are reused in place (rule 1). Missing files download
  into staging and are moved when the torrent finishes.

## Health

Each pass, when `incomplete_path` is set, the root must exist and take a probe write. If it
doesn't:
- **New adds are refused,** with a reason, the same way `disk_low` refuses them.
- **One report problem,** "incomplete folder unavailable", not one per torrent.
- **Recovery:** once the folder is back, torrents stopped with `ErrIncompleteFolderUnavailable` are
  resumed.

---

## Downloads page

- **API:** during phase 1, `GET /torrents` reports `status: "moving"` and `move_progress` (0–1).
  `SessionManager` keeps hash → copied/total bytes and overlays it, the way the queue overlays
  "queued" ([decisions/041](../../agents/decisions/041-the-download-queue-holds-every-incomplete-torrent.md)).
- **Row:** "Moving to library · 42%" ([open question 4](#open-questions)).
- **Filter:** it counts under the Seeding filter, since the download is finished.

## Settings page

In the direct-mode group, below `incomplete_files`: a text field, "Incomplete downloads folder".
Hint: "Optional. Torrents download here and move into the library when they finish. Use an SSD or
NVMe folder, especially when the library is on a USB or SMR hard disk. The folder must already
exist." Plus the pt-BR translation.

## Relation to the qBittorrent backend

The same setting maps onto qBittorrent's own staging: "Keep incomplete torrents in", or the per
torrent download path in its add API (check the exact field and API version in that spec).
qBittorrent then does the copy itself, and the placement only names the final path. The qBittorrent
spec should read `incomplete_path` rather than add a setting of its own.

---

## Decisions this touches

| Decision | Change |
|---|---|
| [136](../../agents/decisions/136-direct-mode-places-a-torrent-before-the-add-and.md) frozen file map | amend: the staging root is frozen with the map |
| [137](../../agents/decisions/137-direct-mode-deletes-through-the-torrent-and-keep.md) deletes through the torrent | amend: also removes staging and `.aad-moving` |
| [139](../../agents/decisions/139-a-direct-mode-file-deleted-outside-the-app-is.md) missing finished file | unchanged for `Completed`; a staged file deleted before completion is "not written yet" and simply downloads again |
| [140](../../agents/decisions/140-redownloading-missing-files-adds-the-same-torrent.md) redownload | amend: `reopen` takes the current `incomplete_path` |
| [093](../../agents/decisions/093-the-disk-guard-has-two-triggers-and-the-size-one.md) / [101](../../agents/decisions/101-the-disk-guard-counts-what-the-queue-still-has-to.md) disk guard | amend: two volumes |
| [024](../../agents/decisions/024-a-failed-torrent-is-dropped-from-the-session-and.md) failure drop | unchanged: staged files are kept like library ones |
| new | "A finished staged torrent is copied while it seeds and switched in a short stop": copy outside the session lock, a final name only by renaming a complete copy, `Open` prefers the final path |

## Tests

- **Placement:** the three `Open` rules; a missing root returns the error and is never created;
  `DiskPath` for staged placements.
- **Real rain** (`session_direct_test.go`):
  - a download lands in staging; finalize moves it into the library; a second peer downloads from
    the library copy;
  - the copy path, forced through a test hook (tests can't count on two filesystems), and the
    rename path;
  - a crash after the renames but before the save: `Open` uses the final paths, and organize
    finishes without copying again.
- **Restart mid-copy:** the `.aad-moving` file is overwritten, and the torrent seeds from staging
  meanwhile.
- **Disk guard:** the two-volume math, with a fake `DiskSpace`.
- **Sweep:** staging leftovers are removed after the grace period; Kept, live and non-hash folders
  are untouched.
- **Redownload with staging:** a pack with one missing file stages and moves only that file.
- **Health:** an unavailable root blocks adds, and its torrents resume when it comes back.
- **API:** validation (relative, missing, inside the library, ignored in hardlink mode) and the
  status fields.
- **Frontend:** the setting renders only in direct mode, and the "moving" row.

## Delivery

One PR in about three commits:
1. placement, storage and the move, with tests;
2. disk guard, sweep and health;
3. settings, status and the UI.

Whether it goes on a branch is [open question 5](#open-questions).

## For this user's setup

```
library_mode          direct
completed_anime_path  /mnt/sda1/Anime              # the Seagate: USB, spinning, likely SMR
incomplete_path       /mnt/nvme01/aad-incomplete   # NVMe; create it once with mkdir
```

The NVMe only needs room for what downloads at the same time: about `max_concurrent_downloads` ×
the usual torrent size, with packs being the big ones.

---

## Settled by this design

Say so if any of these should change.

- **Seeding continues during the copy;** the torrent stops only for the switch.
- **Existing torrents are not migrated** when the setting is turned on.
- **One folder per infohash** in staging, mirroring the library layout inside it.
- **The app never creates the staging root** (unmounted-drive protection).

## Open questions

1. **Hardlink mode too?** Recommended: no, direct mode only.
2. **`.part` names inside staging?** Recommended: follow `incomplete_files`, as in the library today.
3. **One `min_free_disk_gb` for both drives,** or a separate floor for the staging drive?
   Recommended: one.
4. **"Moving · 42%" on the Downloads row?** Recommended: yes. Without it, a pack copying to a USB
   disk looks stuck for minutes.
5. **Branch for the implementation:** `feat/incomplete-folder` (recommended: it's several commits)
   or straight to master.
