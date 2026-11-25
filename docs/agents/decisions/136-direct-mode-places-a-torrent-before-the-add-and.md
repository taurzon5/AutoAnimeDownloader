# 136. Direct mode places a torrent before the add and freezes its file map at the first prepare

**Location:** `internal/torrents/placement.go` — `placementStore` (`register` keeps the first
placement, `placements.json`), `planFiles`, `placementProvider` (the legacy fallback),
`placedStorage.Prepare`/`Open` (the crash-recovery branch), `Placement.Resolve`, `finalizeFiles`;
`internal/torrents/session.go` — `rainConfig` (`CustomStorage` only with placements),
`Session.FinalizePlacement` (stop, rename, start); `internal/torrents/sessionmanager.go` — the
provider stays while any placement exists; `internal/daemon/placement.go` — `registerPlacement`
before every `backend.Add`. Pinned by `TestDirectMode_RealTransferLandsInTheLibrary` (two real rain
sessions on loopback), `TestPlacementStore_RegisterKeepsTheFirstPlacement`,
`TestPlacedStorage_PrepareKeepsTheFrozenMap`,
`TestPlacedStorage_OpenFindsTheFinalNameAfterACrashMidFinalize`,
`TestPlanFiles_ExistingOrClaimedDestinationGetsTheHash`,
`TestPlacementProvider_FallsBackToTheLegacyLayout`, `TestPlacedStorage_FollowsAMovedLibrary`
and `TestRainConfig_DirectModeInstallsThePlacementProvider`.

**What it looks like:** a JSON file of paths kept next to the resume database, written before the
torrent even exists in rain and never recomputed — even when the anime's title, the season rule or
`library_mode` itself changes. A second registration of the same hash is silently ignored. A file
that already sits at a destination gets the torrent's short hash appended instead of being used.
And finishing a download stops the torrent and starts it again.

**Why it's right:** every piece follows from rain's timing and from seeding.

- **Before the add.** rain calls `GetStorage(id)` synchronously inside `AddURI`, before any metadata
  and before the daemon writes the episode record. The provider can't learn the destination later,
  so the daemon registers it first, keyed by the info hash (rain's id here, `Session.Add` passes
  `ID: hash`).
- **Frozen at the first `Prepare`.** rain hands over the full file list once the metadata arrives
  and calls `Prepare` again before every allocation and on every restart. A file that is seeding
  must stay where it is: recomputing would move it out from under rain's bitfield (which would then
  re-download, or worse, seed zeros). So the first plan is saved and later calls only check the file
  count. `register` keeps the first placement for the same reason: a retry after a failure, the
  pass picking the same pack again, or a re-download after a delete with `keep_data`, at any later time (the
  placement was marked `Kept` and is never forgotten, [#137](137-direct-mode-deletes-through-the-torrent-and-keep.md))
  must resume into the files that exist instead of planning a `[hash8]` copy next to them.
- **Relative to the library.** Every stored path is relative to the library root and resolved
  against the configured `completed_anime_path` (the parent of rain's `DataDir`) when a file is
  opened. Absolute paths would make a library the user moves and re-points the config at
  re-download everything at the old location, which
  [#34](034-a-download-folder-swap-is-detected-by-a-dual.md) rules out for hardlink mode.
- **An occupied destination is never adopted.** rain opens an existing file as this torrent's data
  and overwrites the pieces that don't hash. A same-named episode from another release, or a file
  another placement owns, would be corrupted, so the new torrent gets `name [hash8].ext`.
- **The provider only when needed.** `CustomStorage` replaces rain's own file storage for every
  torrent of the session. Hardlink mode with no placements keeps rain's storage untouched, so the
  existing path is byte-for-byte what it was. Once a placement exists the provider stays even with
  direct mode off: those files are not under `.torrents`, and rain's storage would re-download them
  there. Torrents with no placement get `legacyStorage`, a copy of rain's `<DataDir>/<id>` layout
  (rain keeps its own in an internal package), so switching modes never strands a seed.
- **Stop, rename, start.** `.part` files exist so a media server never scans a half-written
  episode. Windows refuses to rename an open file, so the torrent is stopped (rain's stop is
  asynchronous; it waits for `Stopped`), renamed, marked `Completed`, and started to keep seeding.
  A crash between the rename and the save leaves the final name on disk with the placement still
  incomplete: `Open` then uses the final name. Without that branch it would create an empty `.part`:
  rain trusts its bitfield only when no file is missing (`torrent_allocation.go`), so it would
  re-check the torrent and download the whole file again while the data sits under its final name.

**Amendment (see [#141](141-staged-downloads-are-copied-into-the-library-while.md)):** the placement also freezes `IncompleteRoot`, the staging folder, when
`incomplete_path` is set. It is the one absolute path a placement stores (it sits outside the
library, so "relative to the library" can't hold it), and only two places re-point it: `reopen`
(a redownload takes the current setting) and `adoptMovedStaging` (the user moved the folder; the
torrent is added back so rain re-checks the files). `Open`'s crash branch became one rule for any placement not yet
`Completed`: a whole file at the final path is used there; otherwise the staging path, the `.part`
name or the final name. `FinalizePlacement` of a staging placement copies the files into the
library instead of renaming `.part` files.

**Don't "fix" by:** recomputing the map on load "so a renamed anime moves too" (moves a seeding
file); storing absolute paths; letting a second `SetPlacement` overwrite the first; opening an existing destination as-is;
installing `CustomStorage` unconditionally (hardlink mode would stop being what it was) or dropping
it when direct mode is turned off while placements exist; renaming `.part` files while the torrent
runs; deleting the `Open` fallback to the final name.

**Amendment (see [#154](154-several-download-roots-each-placement-records-its.md)):** a placement in a download root other than the library stores a second absolute path, `RootPath` (and `RootName`). A placement in the library keeps it empty, so the moved-library rule above still holds for it; a moved root is followed by name (`followMovedRoots`).
