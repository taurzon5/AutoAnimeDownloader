# 154. Several download roots: each placement records its root, the disk budget picks the first root with space after the anime's override and its series' root, and roots on one drive share its space

**Location:**
- `internal/files/filemanager.go`: `Config.DownloadRoots` (`download_roots`), `DownloadRoot`,
  `Config.Roots` (download_roots in direct mode, else the library alone, `LibraryRootName`),
  `Config.RootNamed`, `Config.RootMinFreeGB` (`DownloadRoot.MinFreeGB`), and `AnimeSettings.Root`.
- `internal/torrents/placement.go`: `PlacementRequest.RootPath`/`RootName`, `Placement.RootOr`;
  `Resolve` resolves against the placement's own root; `ownerOf(root, library, path)` claims a path
  per root. rain's `placedStorage` (`Prepare`, `RootDir`) and qBittorrent's `Add` (save path),
  `applyPlacement` and finish waits use the placement's root.
- `internal/daemon/placement.go`: `rootOrder` (override → the root holding the series → the list's
  order), `availableRoots`, `placedOrder`, `followMovedRoots` and `registerPlacement(…, root)`.
- `internal/daemon/diskbudget.go`: `withRoots`, `reserve(configs, tr, order)` (returns the root),
  `check` (any root), `checkOn` (one root), `onRootLocked` (every root on the same filesystem);
  `helpers.go`: `checkRootBudgetSpace`, `RootDisks`.
- `internal/api`: `validateDownloadRoots`, `PUT /animes/{id}/settings` `root`, `GET /status`
  `disks`, `qbitTestFolders` (the connection test checks each root); the CLI's
  `animes settings <id> root <name|none>`; the settings page's Download Folders (the library's row,
  `findLibraryRow`), `AnimeRootPicker.svelte`, the Status page's disk card.
- `internal/daemon/health.go`: `roomiestRoot`, the root the `disk_low` alert watches;
  `helpers.go`'s `RootDisk.Drive` (the first root on the same filesystem), which `GET /status`
  `disks` carries as `drive`.

Pinned by:
- `TestRootOrder`, `TestDiskBudgetFillsRootsInOrder`,
  `TestDiskBudgetRootsOnOneDriveShareTheSpace`, `TestRegisterPlacementRecordsItsRoot`,
  `TestFollowMovedRoots`, `TestPlacedOrderKeepsTheTorrentsRoot`,
  `TestReorderingRootsMovesNothing`, `TestRootsFillUpToTheirOwnLimit` and
  `TestCheckRootBudgetSpace_TwoVolumes`.
- `TestPlacedStorage_OpenRefusesAMissingDownloadRoot` and
  `TestQBit_TorrentWaitsForItsDownloadRootAndRestarts` (torrents).
- `TestUpdateConfig_ValidatesDownloadRoots`, `TestUpdateConfig_DownloadRootsUnderThePathMap`,
  `TestPutAnimeSettings_Root`,
  `TestHandleStatus_DisksPerRoot` and `TestAnimesSettingsRoot` (CLI).
- `TestRootDisksMarksFoldersOnOneDrive` and the Status page's "download folders on one drive show
  as one line" test.
- `TestDiskAlertVolume_WatchesTheRoomiestDownloadFolder`,
  `TestTestQBittorrent_ProbesEachDownloadFolder` and the settings page's
  `Config — the library among the download folders` tests.
- The frontend's `Config.roots.test.ts` and `AnimeRootPicker.test.ts`.

**What it looks like:**
- A placement in the library has no root path at all, while one anywhere else has an absolute one.
- The root is chosen inside the disk budget, not by the placement code.
- Two roots that are folders of one drive can't each take a torrent the drive only fits once.
- `completed_anime_path` stops being a download folder once `download_roots` lists any.

**Why it's right:**

- **Asked by the user** (2026-11-24): several download folders now, on their own branch, as the
  direct-library-mode spec's PR 2. Once this branch met `feat/shoko-sync`, each
  folder says whether it is in one of Shoko's import folders, read from Shoko through
  `shoko_path_map` ([#152](152-a-library-that-is-a-shoko-drop-folder-releases.md)): a torrent is
  handed to Shoko by the root its placement is in.
- **The root lives in the placement.** Paths stay relative to their root. A moved library keeps its
  seeds ([#136](136-direct-mode-places-a-torrent-before-the-add-and.md)), and a moved root is
  followed by name (below). Every
  backend already resolves through `Placement.Resolve`, so giving the placement its root makes them
  all follow; the few places that joined the library root by hand now use `RootOr`. A placement
  from before has no root path and keeps resolving against `completed_anime_path`, and so does a
  new one placed in the library: that is what a moved library relies on.
- **The disk budget chooses.** "First root with space" needs what each root still has to write
  ([#101](101-the-disk-guard-counts-what-the-queue-still-has-to.md)), which only the budget
  knows, and the pass reserves packs before the add (`chooseCoveringPacks`). So `reserve` takes the
  order and returns the root it reserved on, and the add places the torrent there. A pack's
  episodes go where the pack went (`countedRoot`). The override travels in the budget too
  (`withRoots`), so `processAnimeEpisodes` keeps its signature; manual downloads read it from the
  anime's settings (`torrentTarget.root`).
- **The order.** The anime's override first, then the root already holding the series (a new
  season lands next to the others), then `download_roots` as listed: the first is the highest
  priority, reordered on the settings page. The user asked for "one drive should fill up to certain
  extent (manageable for each drive), then move to the next" (2026-11-24), so each root keeps its own
  `min_free_gb` (`Config.RootMinFreeGB`, `min_free_disk_gb` when unset), and the budget's "first
  root with space" turns that into filling one drive up to its limit before the next. A root without
  space passes the torrent on; none with space refuses it, as one full library did. A "most free
  space first" order was built and dropped the same day: the user wants one drive filled before the
  next. `series_drive_first` only decides between the series' root and `folder_rules`, which are PR
  3, so it waits for them.
- **The incomplete folder on a root's drive** takes every root's staging downloads, so that root
  also counts the ones bound for other roots (`stagedOtherBytes`); the old shortcut, nothing more
  on the same drive, held only while the library's reservations covered every torrent.
- **A moved root is re-added in rain**, as a moved incomplete folder is: the torrent verifies the
  files where they are now (`Session.MovePlacementRoot`, #140).
- **One drive, one free space.** Two roots on the same filesystem would otherwise each count the
  drive's free space as their own, and fit two torrents where one fits. `onRootLocked` adds up what
  every root on that filesystem reserved.
- **A moved root is followed by name.** A placement outside the library stores its root's absolute
  path, unlike one in the library. When the user moves such a root and edits `download_roots`, the
  pass re-points its placements by `RootName` once the old folder is gone and the new one is there
  (`followMovedRoots`). A root that is merely gone is left alone: its drive may be unmounted.
  qBittorrent keeps the save path it was given, so a root moved under qBittorrent is moved there
  too.
- **A root must exist, always.** Validation requires it, `availableRoots` leaves out one that is
  gone (so `rootOrder`, `check` and `reserve` never pick it, and `placedOrder` refuses a re-add into
  it), rain's `Open` refuses to write into it (`ErrDownloadRootUnavailable`), and qBittorrent's poll
  stops a torrent there and never starts one (`Placement.missingFolder`, `guardStaging`,
  `stopError`): a torrent there waits, and is started again once the folder is back, as for the
  incomplete folder ([#141](141-staged-downloads-are-copied-into-the-library-while.md)).
  An unmounted drive would otherwise fill the disk its mount point sits on.
- **Reordering moves nothing.** The list's order is read only where a new torrent is placed
  (`rootOrder`). Each placement keeps the root it was given, by path, and nothing moves a torrent
  between roots except a moved root followed by name (above), which needs its old folder gone: a
  reorder swaps whole entries, so no name points at another path. A series' new episodes keep going
  next to it even after its root drops down the list.
- **A torrent added again stays where it is.** `register` keeps a hash's first placement (#136), so
  a retry or a re-add writes to its old root. `placedOrder` makes the budget count it there.
- **The screen agrees with the gate.** `DiskLow` and each root's `low` in `GET /status` go through
  the same per-root budget; a redownload counts its bytes on its own root (`addOn`); a manual
  download is refused before the search only when no root has space. With a qBittorrent path map,
  every root must be under one of its app folders: it is the torrent's save path there.

**Not done yet:** the folder-swap marker per root
([#34](034-a-download-folder-swap-is-detected-by-a-dual.md)) still watches the library only;
`series_drive_first` and `folder_rules` are PR 3.

**Done since (2026-11-26):** the `disk_low` alert watches the root with the most room above its own
floor (`roomiestRoot`): roots fill one after another, so a full first root is not news. The
qBittorrent connection test checks each root (`TestQBittorrent`'s `folders`). The settings page
shows the library as the root whose path it is, and edits it there, instead of a second field with
the same path.

**One line per drive on the Status page (2026-11-26).** Roots on one filesystem share its free
space, so the disk card shows them on one line, grouped by `drive`. Each root's name keeps its own
`low`: roots on one drive can have different floors, and one can be full while the other still takes
torrents, as the gate places them.

**Known limit of the alert:** it watches one root, the roomiest. A write tied to one root, a re-add
or a redownload on its own root, or an older placement in the library, can be refused there while
another root has room; the `blocked` alert then names the roomiest root, which has room. And a
library that is not one of the listed roots is no longer watched by it. Naming the refused root
needs the pass to say which root refused, which it doesn't today.

**Don't "fix" by:**
- Storing the library as a root path in new placements: a moved library would strand them.
- Choosing the root outside the budget: a pack's reservation and its add could pick different roots.
- Counting each root's free space on its own when roots share a drive.
- Ordering roots by free space: a drive fills to its own limit before the next one is used.
- Creating a download root that doesn't exist, at save or when a torrent opens a file.
- Re-pointing a root's placements while its old folder is still there.
- Budgeting a re-added torrent on another root than its placement's.
- Moving placed torrents when the order changes, to "rebalance": the order is for new torrents,
  and re-pointing a torrent at another root makes rain download it again there.
