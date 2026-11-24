# 145. With qBittorrent a staged torrent downloads into its incomplete folder as qBittorrent's download path, qBittorrent moves it into the library, and the app then clears the path

**Location:** `internal/torrents/qbittorrent.go`:
- `Add`: `DownloadPath` from `Placement.StagingDir`.
- `applyPlacement`: staged files keep their final names.
- `FinalizePlacement` → `finalizeStaged`: waits for the move, then clears the path.
- `RedownloadPlacement`: in place, with the path cleared first.
- `MovePlacementStaging`: `setDownloadPath` plus a recheck.
- `guardStaging`, the missing-folder check before a start in `applyPlacement`, and `stopError`
  (`ErrIncompleteFolderUnavailable`).
- `withMoveProgress`, and `qbitStatusSlug` (`moving` → `StatusMoving`).
- `TestQBittorrent`'s second probe.

`internal/torrents/qbit_client.go`: `setDownloadPath` and `useDownloadPath` in `add`.
`internal/api/endpoint_config.go`: `incomplete_path` must be under a path-map `app` folder.

Pinned by:
- `TestQBit_StagedTorrentDownloadsInTheIncompleteFolderAndMovesIn`
- `TestQBit_MovedIncompleteFolderIsFollowed`
- `TestQBit_StagedTorrentWaitsForItsFolderAndRestarts`
- the in-place part of `TestQBit_RedownloadRechecksAndStarts`
- `TestTestQBittorrent_ProbesTheLibrary`
- `TestUpdateConfig_IncompleteFolderIsAllowedWithQBittorrent`
- `TestQBitLive`, whose single episode stages

**What it looks like:** with rain, a staged torrent is copied into the library while it keeps
seeding and switched over in a short stop ([#141](141-staged-downloads-are-copied-into-the-library-while.md)).
With qBittorrent, the app does none of that: qBittorrent moves the files itself, and the torrent
doesn't seed while it moves.

Three other things look odd:
- A redownload ignores `incomplete_path` and writes the missing files straight into the library.
- After the move, the app clears a torrent's download path, although the files are already in
  the library and the call seems to change nothing.
- The app stats files to report "Moving · NN%".

**Why it's right:**

- **qBittorrent keeps all of a torrent's files in one place.** That place is the save path, or
  while incomplete, the per-torrent download path. The app can't put each file where it wants, as
  rain's placed storage does. With the download path set to `<incomplete_path>/<hash>`, qBittorrent
  writes the torrent exactly where `Placement.StagingPath` says, under the names the app renamed
  the files to. When the torrent finishes, it moves the files into the save path, the library
  root, by itself.

  Measured on qBittorrent 5.3.0. The move is a sequential copy across drives, which is the point of
  #141 for an SMR library disk. The cost is that the torrent doesn't seed during the move, which is
  accepted. As in #141, staged files have no `.part`: the incomplete folder is out of Jellyfin's
  sight.
- **Finishing waits for qBittorrent's move.** `FinalizePlacement` accepts only a settled torrent,
  and `moving` is not settled. Then it waits `moveWait` for every file at its final path, because
  qBittorrent can report the torrent finished just before the move starts. While the move runs,
  finishing fails and organize retries with its backoff.
- **The app measures move progress.** qBittorrent doesn't report one, so `MoveProgress` is the
  bytes at the final paths out of each file's larger copy. During a copy a file is partly at its
  final path and still whole in staging. It's measured outside the backend's lock, so a slow disk
  can't hold up the poll.
- **The download path is cleared after the move.** Measured: when a finished torrent with its
  download path still set was rechecked and came out incomplete (one file deleted), qBittorrent
  moved *all* its files out of the library into the download path, to download the missing piece
  there. So `finalizeStaged` clears the path once the files are in. Any later recheck then leaves
  the library alone.

  For the same reason `RedownloadPlacement` clears the path before its recheck and downloads in
  place. Redownloading into staging would move a whole pack out of the library and back for one
  missing episode. The trade-off: the missing files are written in random order on the library
  disk.
- **A moved incomplete folder is followed with `setDownloadPath`.** Measured: pointing the download
  path at a folder the user had already moved keeps the files there, and a recheck found the data
  (57% of a pack). Without the recheck, qBittorrent showed 0%.
- **A staged torrent never runs while its incomplete folder is gone.** Rain refuses to create the
  folder ([#141](141-staged-downloads-are-copied-into-the-library-while.md)). qBittorrent creates a
  missing download folder whenever its user can write the parent, so with an unmounted drive it
  would recreate `<incomplete_path>` on the system disk. Measured on 5.3.0: under a writable parent
  it created the folder and downloaded; under a root-owned mount point the torrent went to `error`.
  So the app holds staged torrents back itself:
  - `applyPlacement` doesn't start one whose folder is missing.
  - `guardStaging` stops a running or queued one (qBittorrent's queue would start it). A torrent
    still fetching its metadata writes nothing and stops by itself, so it's left alone.
  - A torrent that fails gets `ErrIncompleteFolderUnavailable` from `stopError`, so
    `HandleTorrentFailure` waits instead of dropping and re-adding it.
  - Once the folder is back, `guardStaging` rechecks and starts the torrents it stopped, held back
    or saw fail. With rain, the download queue restarts those on every pass; nothing does that for
    qBittorrent. A torrent the user stopped stays stopped.

  The limit: a torrent writing at the moment the drive goes away can recreate the folder before
  the next poll, at most 2 s later.
- **qBittorrent must see the incomplete folder too.** It writes there itself. So with a path map,
  `incomplete_path` must be under an `app` folder of the map, and the connection test probes the
  incomplete folder after the library.

**Don't "fix" by:**
- Copying the staged files in the app.
- Giving staged files a `.part` name.
- Leaving the download path set after the move.
- Redownloading into the incomplete folder.
- Re-pointing a moved folder without the recheck.
- Mapping `moving` back to `verifying` (the row would lose its move line).
- Removing the wait in `finalizeStaged` because `moving` is already refused.
- Starting a staged torrent without checking its folder.
- Restarting a torrent the user stopped.
- Stopping a torrent that is still fetching its metadata.
