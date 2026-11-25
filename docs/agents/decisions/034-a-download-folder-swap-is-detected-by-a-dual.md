# 34. A download-folder swap is detected by a dual marker, and it tears down the rain session

**Location:** `internal/torrents/rootmarker.go`, `internal/torrents/sessionmanager.go` (`Ensure`, `checkRoot`, `writeRoot`), `internal/daemon/verification.go` (`clearLibraryPathsAfterRootSwap`), `internal/daemon/migration.go` (the marker travels with the rename). Pinned by `TestSessionManagerDetectsRootSwap`, `TestSessionManagerRootSwapReportedOnce`, `TestSessionManagerMovedAndRepointedIsNotASwap`, `TestSessionManagerFirstRunAdoptsExistingFolder` and `TestClearLibraryPathsAfterRootSwap`.

**What it looks like:** there are two files with the same random id — `.aad_root` **inside** the download folder and `download_root.id` in the config folder. `Ensure` compares the two on every pass and, when they diverge, closes and recreates the rain session.

**Why it's right:** rain holds open *file descriptors*. When the user moves the download folder (or sends it to the trash) with the daemon running, bytes keep landing on the inode that left while the configured path sits empty — the UI shows everything seeding and **every** `JobOrganize` fails with `no such file or directory`, burning through the 20 retries in silence. Nothing in the process notices, because the path string did not change: only the inode behind it did.

The pair of markers is what makes this detectable without depending on inode (which does not exist on Windows):

- the **inside** marker travels with the folder, so it disappears from the configured path exactly when the folder is moved;
- the **outside** id stays where the user does not touch it, so it survives as a reference — including when the folder is moved with the daemon **stopped**.

Recreating the session is the complete fix because rain already does the hard part: in `torrent_allocation.go`, if the files do not exist (`al.HasMissing`), it **discards the resume bitfield** and re-downloads. There is no need to touch the resume db or remove any torrent.

**Why the signal is a latch and not `Ensure`'s return:** the manual-download endpoints also call `Ensure`. If the flag came back in the return, one of those calls would swallow the swap — the session would be recreated, but the orphan library records would never be cleared, and recovery would be left half-done with no visible error. `Ensure` sets `pendingSwap`; only `ConsumeRootSwap`, called on the verification pass, clears it. Pinned by `TestSessionManagerRootSwapSurvivesAnotherEnsure`.

Three properties that fell out for free and are intentional:

- **Moving the folder AND re-pointing the config** (`completed_anime_path` to the new place) is **not** a swap: the marker travelled with it, the ids match, nothing is re-downloaded. That is the "right" way to move the library.
- **First boot / upgrade from a version without a marker** is not a swap: with no reference id there is nothing to compare, and adopting the folder as-is is the only safe reading — the alternative would wipe the records of a healthy library.
- **It is edge-triggered:** `Ensure` reports the swap once and already rewrites the marker, so the next pass is back to normal.

**Amendment to decision 29:** `clearLibraryPathsAfterRootSwap` zeros `LibraryPaths` — exactly what #29 forbids. The prohibition still holds for a missing file: what it protects is the deliberate deletion of **one** episode from the library, which a `Stat` check every pass would resurrect forever. A root swap is a different event: the entire folder the records point at is gone, the daemon is already re-downloading its contents, and detection fires **once per swap** — it never becomes #29's resurrection loop. Without this cleanup recovery is half-done: the torrents re-download, but episodes organized before the swap keep an orphan `LibraryPaths` and never return to the library.

**Amendment (see [#136](136-direct-mode-places-a-torrent-before-the-add-and.md)):** in direct mode the torrents' files are in the library itself, and
placements store their paths **relative to the library root**, resolved against the configured
path when rain opens them. Moving the library and re-pointing the config at it therefore keeps
every direct-mode torrent seeding, exactly like the hardlink case above. The swap check itself is
unchanged: the marker still lives in `<library>/.torrents`.

**Amendment (see [#139](139-a-direct-mode-file-deleted-outside-the-app-is.md)):** in direct mode the recreated session does not re-download a
finished torrent's files from a replaced or vanished library: the placement storage refuses to
recreate a completed placement's missing file, the torrent stops, organize leaves it alone (no
LibraryPaths, no completion webhook) and the Downloads row shows the files missing. Torrents added
in hardlink mode are re-downloaded as described above, and `library_missing` says which applies.

**Don't "fix" by:** returning the flag from `Ensure` instead of the latch (see above); replacing the markers with inode comparison (`syscall.Stat_t.Ino` does not exist on Windows); treating a marker-read error as "it vanished" (a permission failure would start wiping library records — that is why `readRootID` only swallows `IsNotExist`); cleaning `LibraryPaths` on every pass instead of only on a swap (that really would be the #29 violation); ~~dropping the marker rename in `MigrateSavePath`~~ (the migration was removed on 2026-08-24 — see #31; if it comes back, the marker rename becomes mandatory again for the same reason).

**Amendment (see [#154](154-several-download-roots-each-placement-records-its.md)):** the marker and the moved-library rule cover the library and the placements in it (no `RootPath`). A placement in another download root stores that root's absolute path; when the user moves such a root and edits `download_roots`, `followMovedRoots` re-points it by its `RootName` once its old folder is gone. There is no swap marker per root yet.
