# 141. Staged downloads are copied into the library while they seed and switched over in a short stop

**Location:** `internal/torrents/staging.go` (`copyStagedFiles`, `Session.finalizeStaged`,
`moveTracker`); `internal/torrents/sessionmanager.go` — `SessionManager.FinalizePlacement` and
`copyStaged` (the copy without `m.mu`), `CancelMoves`, the cancel in `Remove`, the `moving`
overlay in `List`/`Get`; `internal/torrents/placement.go` — `PlacementRequest.IncompleteRoot`,
`Placement.DiskPath`/`StagingPath`/`StagingDir`, the rules in `placedStorage.Open`,
`ErrIncompleteFolderUnavailable`, `RemovePlacedFiles`; `internal/files/probe.go` —
`ProbeIncompleteFolder`, the disk-full case in both probes; `internal/daemon/helpers.go` — `checkBudgetSpace`, the early return in
`HandleTorrentFailure`; `internal/daemon/diskbudget.go` — `staged`/`stagedPending`;
`internal/daemon/placement.go` — `resumeTorrentsWaitingForStaging`, `adoptMovedStaging`,
`pruneStagingLeftovers`; `internal/daemon/health.go` — `diskAlertVolume`;
`internal/daemon/verification.go` — the incomplete-folder probe; `cmd/daemon/main.go` — the
`CancelMoves` defer. Pinned by `TestDirectMode_StagedDownloadMovesIntoTheLibrary` (real rain
sessions, copy and hard-link paths), `TestPlacedStorage_StagingWritesInTheIncompleteFolder`,
`TestPlacedStorage_OpenPrefersAWholeFinalFile`, `TestMoveTracker_OverlaysMovingProgress`,
`TestCheckBudgetSpace_TwoVolumes`, `TestPruneStagingLeftovers`,
`TestResumeTorrentsWaitingForStaging`, `TestHandleTorrentFailure_LeavesAStagingWaitAlone`,
`TestAdoptMovedStaging`, `TestCopyStagedFiles_FailureLeavesNoCopies`,
`TestDiskAlertVolume_FollowsTheTighterDrive` and `TestProbeIncompleteFolder`.

**What it looks like:** `incomplete_path` (direct mode only) makes a torrent download into
`<incomplete_path>/<hash>/<library-relative path>`. When it finishes, its files are copied into the
library as `<final>.aad-moving` while the torrent keeps seeding from the staging copy; then the
torrent stops for a moment, every `.aad-moving` is renamed to its final name, the placement is
marked `Completed`, and the torrent starts again seeding from the library. A plain `os.Rename`, or
stopping the torrent for the whole copy, looks simpler.

**Why it's right:** the user's library is on a USB hard disk that is very likely SMR (shingled).
Downloading writes pieces in random order, and on an SMR drive that fills the drive's small fast
cache and then rewrites a whole band per write: a few MB/s, with stalls of seconds. Staging on an
NVMe and moving the finished file turns all of that into one sequential write per file.

- **A copy, not a rename.** Staging is on another drive; a rename across filesystems fails. A hard
  link is tried first: on one filesystem it is the whole copy, instant and free, and the two
  volumes collapse into one for the disk guard too.
- **Copy while seeding.** A finished torrent only reads, so it keeps seeding from staging during a
  copy that takes minutes for a pack on a USB disk. It stops only for the renames.
- **Outside the session lock.** `FinalizePlacement` used to run entirely under `SessionManager`'s
  read lock. A copy of minutes there would let a waiting writer (`Ensure`) block every `List`
  behind it, and the web UI would freeze. The copy runs without the lock; only the switch takes it.
- **One copy at a time.** `moveTracker` serializes copies: two interleaved sequential writes are
  random writes again on an SMR disk. Organize jobs already run one by one; the mutex keeps it
  true for any other caller.
- **A final name only by renaming a whole, synced copy.** That is what lets `placedStorage.Open`
  take a file of the right size at the final path for a placement not yet `Completed` (rule 1): a
  crash between the renames and the save leaves exactly that, and organize then copies only what
  is not in place. The same rule reuses a library file during a redownload
  ([#140](140-redownloading-missing-files-adds-the-same-torrent.md)). Trusting rain's stored
  bitfield after the switch is safe for the same reason.
- **Never `.part` in staging** (the user's choice): nothing scans the staging folder, so files
  keep their final names there; the library only ever sees whole files.
- **The staging root is never created.** If its drive is not mounted, the mount point is an empty
  directory on the system disk, and creating the folder there would quietly fill it. `Open`
  returns `ErrIncompleteFolderUnavailable`; `HandleTorrentFailure` leaves the torrent alone
  (dropping and re-adding would fail the same way); the pass stops with the `incomplete_folder`
  code; once the folder is back the download queue restarts unfinished torrents on its own, and
  `resumeTorrentsWaitingForStaging` restarts finished ones (the queue never touches those).
- **Two volumes, two floors.** A new download must fit on the staging volume (what staging
  torrents still have to download, floor `incomplete_min_free_disk_gb`) and on the library volume
  (plus what staging torrents hold and have not moved yet, floor `min_free_disk_gb`) — the user
  asked for separate floors.
- **Frozen per placement.** `IncompleteRoot` is copied from the config when the placement is
  registered ([#136](136-direct-mode-places-a-torrent-before-the-add-and.md)); changing or
  clearing the setting only affects new torrents. A redownload takes the current setting.
- **Shutdown and delete cancel the copy.** `JobQueue.Stop` waits for the organize job in progress;
  `CancelMoves` runs first. A cancelled copy removes its `.aad-moving` and starts over next time.

**A moved incomplete folder is followed.** The frozen root is absolute (it is outside the
library), so moving the folder is handled like a moved library
([#34](034-a-download-folder-swap-is-detected-by-a-dual.md)): `adoptMovedStaging` re-points an
unfinished torrent whose root is gone when its `<hash>` folder sits in the configured
`incomplete_path`, and adds it back so rain re-checks the moved files: a folder copied while the
torrent was still writing is older than rain's saved progress, and a plain start would trust that
progress (the same trap as [#140](140-redownloading-missing-files-adds-the-same-torrent.md)). The
sweep never removes an unfinished torrent's folder, whatever root it was registered with. **Known limit:** a root that is gone with the files nowhere else (or after a
switch back to hardlink mode, which has no `incomplete_path`) leaves that torrent waiting,
logged every pass; it is not a report problem. **Known limit:** the never-create rule assumes
`incomplete_path` is a folder *inside* the drive. If it is the mount point itself and the daemon
may write there (root in a container, a mount directory owned by the user), an unmounted drive
still looks like an existing folder, and downloads land on the system disk. The setting's hint
asks for a folder inside the drive.

**A full drive is reachable.** Both folder probes treat "disk full" as a pass: aborting the pass
would skip the cleanup that frees space ([#48](048-max-episodes-per-anime-never-limited-batches-the.md)), and
`checkBudgetSpace` already refuses the downloads. The `disk_low` alert follows whichever drive has
less room above its floor (`diskAlertVolume`).

**Don't "fix" by:** renaming staging files straight into the library (fails across drives, and
on one drive a crash mid-way leaves a placement whose staged file is gone); copying under
`m.mu`; running copies in parallel; creating `IncompleteRoot` (or using `MkdirAll` on it) when it
is missing; giving a library file its final name before the copy is synced; dropping or
re-adding a torrent stopped with `ErrIncompleteFolderUnavailable`; checking only the library
volume when staging is on another drive.

**Amendment (see [#145](145-with-qbittorrent-a-staged-torrent-downloads-into.md)):** with `torrent_client: "qbittorrent"` the copy-while-seeding above is
rain's only. qBittorrent downloads a staged torrent into the same `<incomplete_path>/<hash>/` as its
per-torrent download path and moves it into the library itself when it finishes, not seeding
during the move. `FinalizePlacement` waits for that and clears the download path, a redownload
writes into the library directly, and "never `.part` in staging" holds. "Never create the
folder" holds only because the app keeps staged torrents stopped while their folder is missing:
qBittorrent itself creates a missing download folder when it can write the parent (measured).
