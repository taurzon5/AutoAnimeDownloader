# 139. A direct-mode file deleted outside the app is reported and not downloaded again on its own

**Location:** `internal/torrents/placement.go` — `ErrPlacedFileMissing`, the `p.Completed` check in
`placedStorage.Open`; `Session.FinalizePlacement` (marks an in-place placement `Completed` too);
`internal/daemon/helpers.go` — the early return in `HandleTorrentFailure`; `internal/daemon/jobs.go` —
the early return in `organizeTorrent`; `internal/daemon/verification.go` — `libraryMissingItem`;
`internal/daemon/placement.go` — `PlacedFilesMissing`, `RedownloadMissingFiles`;
`internal/api/endpoint_torrents.go` — `TorrentResponse.FilesMissing`,
`handleTorrentsRedownloadMissing`; frontend `routes/Downloads.svelte` (the note on the torrent row,
the "Redownload missing files" strip) and `components/RedownloadMissingDialog.svelte`.
Pinned by `TestDirectMode_DeletedFinishedFileIsNotDownloadedAgain` (real rain session),
`TestPlacedStorage_OpenRefusesToRecreateADeletedFinishedFile`,
`TestHandleTorrentFailure_LeavesADeletedFilesTorrentAlone` and
`TestHandleTorrentsFlagsADeletedLibraryFile`.

**What it looks like:** a finished torrent that is missing its file is left stopped with an error.
The failure handler sees the error and does nothing: no drop, no re-add, no `download_failed`.
The only warning is one quiet line on that torrent's row in Downloads; getting the file back is
the user's click.

**Why it's right:** in direct mode the library file **is** the torrent's data. When the user
deletes an episode outside the app — from Jellyfin, a file manager — rain, on the next session
open, finds the file missing, re-checks the torrent and downloads it again: the episode the user
just deleted comes back. The user's rule (2026-11-23): **show a warning for the missing file,
nothing else.**

- **No re-download.** `placedStorage.Open` refuses to recreate a file of a `Completed` placement
  and returns `ErrPlacedFileMissing`; rain stops that torrent with the error instead of
  allocating an empty file. `Completed` is set at organize for both `incomplete_files` values, so
  "missing" before completion is still the normal "not written yet".
- **No other action.** `HandleTorrentFailure` returns early for a finished placement with a missing
  file (any of its files, the same set `Open` guards — a deleted subtitle stops the torrent too): dropping it would make the next pass re-add the hash (the record points at it), which
  is the re-download again by another road; a `download_failed` webhook would be the "something
  else". The episode's records stay, so the pass doesn't see it as missing either.
- **The warning, on the torrent itself, and minor.** `GET /torrents` sets `files_missing` on a
  finished placement missing any of its files (`PlacedFilesMissing`), and the torrent's row in
  Downloads shows one muted line, "File not found". The user asked for it there and for no major
  warning: no report problem, no banner, no webhook. A report problem was tried first and dropped
  — it also fed `episode_stuck`, the "something else". The strip above the list (a count and the
  "Redownload missing files" button, muted like the toolbar, not alert-coloured) is the action,
  not a second warning.
- **The way out is the app.** Deleting or redownloading the episode from the web UI or CLI goes
  through the torrent: the removal tolerates the missing file and forgets the placement. Or
  "Redownload missing files" fetches the files again into the same paths, for the torrents the
  user leaves ticked.

**Amendment (see [#140](140-redownloading-missing-files-adds-the-same-torrent.md)):** the bulk
"Redownload missing files" is the way out for many torrents at once, a replaced library folder
above all. A quiet strip above the Downloads list opens a dialog listing every torrent with
missing files, all ticked; the user unticks the ones to leave alone. It is the user's click, never
automatic, so "nothing else" still holds. How it re-fetches (the torrent is added back to rain, not
restarted) and why are in #140.

**A replaced or vanished library folder is the same case.** The swap check
([#34](034-a-download-folder-swap-is-detected-by-a-dual.md)) still clears the records'
`LibraryPaths` and fires `library_missing` (worded for direct mode); the recreated session stops the
finished direct-mode torrents, and `organizeTorrent` returns early for a placement whose files are
missing — no LibraryPaths written back, no `download_completed` for files that don't exist. An
unmounted drive that comes back: the torrents start again on the next session open (a restart),
and organize records the paths then.

**Known limits:** a pack is one torrent, so one deleted file flags (and stops) the whole pack's
row.

**Don't "fix" by:** letting `Open` create the missing file "so the torrent keeps seeding" (that is
the re-download); dropping or re-adding the stopped torrent in `HandleTorrentFailure`; raising the
warning anywhere louder (a report problem, a banner, `download_failed`, `episode_stuck`); deleting
the episode's records automatically; running "Redownload missing files" on its own.

**Amendment (see [#153](153-a-packs-episodes-already-in-shoko-are-removed-for.md)):** a file marked `Skipped` (an episode of a pack removed because the
user has it in Shoko) is deleted by the daemon, not the user. `PlacedFilesMissing` doesn't count it,
and `Open` writes it again when a finished pack is added again, instead of stopping on it: the
daemon deletes it again once the torrent finished. Otherwise the failure handler, which reads
`PlacedFilesMissing`, would drop and re-add the torrent.
