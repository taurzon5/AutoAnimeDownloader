# 137. Direct mode deletes through the torrent, and keep forever spares only direct-mode torrents

**Location:** `internal/daemon/episodes.go` — the placed branch in `removeEpisodesAndLinks`,
`withoutKeptForever` (called from `handleSavedEpisodes` and `deleteEpisodesByStatus`),
`effectiveMax`/`windowEnd`; `internal/daemon/placement.go` — `pruneOrphanPlacements` (end of every
pass); `TorrentBackend.SetPlacementKept` / `Placement.Kept`; `internal/daemon/jobs.go` — `releaseTorrent` (`keepData` for a placed
torrent); `internal/torrents/placement.go` — `placedStorage.Remove`, `RemovePlacedFiles`;
`internal/files/filemanager.go` — `Config.LiftsEpisodeLimit`, `EpisodeStruct.DirectLibrary`; `frontend/src/routes/Config.svelte` — `episodeLimitLifted`, which repeats `LiftsEpisodeLimit` on the unsaved form values to grey out the episode-limit switch ([#158](158-the-episode-limit-stays-one-number-with-0-as-off.md)): the two change together. Pinned by
`TestRemoveEpisodesAndLinks_DirectModeDeletesThroughTheTorrent`, `TestWithoutKeptForever`,
`TestKeepForeverLiftsTheEpisodeLimit`, `TestPlacedStorage_RemoveDeletesFilesAndEmptyFoldersOnly`,
`TestPlacedStorage_RemoveClearsJellyfinLeftovers`, `TestPruneOrphanPlacements`,
`TestRemoveTorrentWithEpisodes_KeepDataKeepsTheFilesFromTheSweep`,
`TestRemoveTorrentWithEpisodes_KeepDataAbortsWhenTheFilesCannotBeKept`,
`TestPruneOrphanPlacements_KeptPlacements`, `TestPlacedStorage_RemoveWithoutMetadata`,
`TestWithoutKeptForever_SurvivesASwitchBackToHardlinkMode`,
`TestPlacementStore_ReAddedKeptPlacementResumes`.

**What it looks like:** for a direct-mode record, removal skips `RemoveFromLibrary` and the folder
cleanup entirely and just removes the torrent. "Keep forever" filters the delete list instead of
turning `delete_watched_episodes` off, and it leaves torrents added in hardlink mode to be pruned
as before. Under `disable_seeding` a direct-mode torrent leaves the session with `keepData=true`,
the opposite of hardlink mode.

**Why it's right:** in direct mode the library file **is** the torrent's data; there is no second
name ([#21](021-embedded-torrent-client-hardlink-into-library.md) does not apply to it).

- **One delete, through the torrent.** `Remove(hash, false)` reaches `placedStorage.Remove`, which
  deletes exactly the mapped files (final and `.part`), prunes the folders it emptied up to the
  library root (a folder left with only Jellyfin leftovers — `.nfo` files, a `.chapters` folder —
  counts as empty, the library sweep's rule) and forgets the placement.
  Deleting the "hardlink" first would race rain, which still has the files open. A **released**
  record (disable_seeding) has no torrent left to remove, so its files are deleted by the same
  `RemovePlacedFiles`, once the whole group goes, and its placement is forgotten.
- **keep_data marks the placement Kept, first.** A torrent removed by hand with `keep_data`
  leaves its files to the user. `SetPlacementKept` runs **before** the removal — and, in
  `RemoveTorrentWithEpisodes`, before blocking the episodes — and when it fails the torrent, its
  records and the blocks stay as they were and the error reaches the caller
  ([#74](074-the-pack-exclusion-guards-unit-is-the-torrents.md): no success that deleted nothing): marking after the fact would leave, on one
  failed save, a placement nothing points at, which is exactly what the orphan sweep deletes. A
  **`Kept` is permanent** — never cleared, and the placement is never forgotten — and it guards
  exactly two paths: the orphan sweep skips it, and a removal before the metadata arrived (rain
  passes no manifest: a re-added hash that stalled) deletes nothing. That is all it does. Re-adding
  the same hash reuses the placement and resumes into the files, whenever it happens; from then on
  the torrent follows the usual rules like any other (a normal delete, or automatic deletion with
  `auto_delete_downloads` on, deletes it). Clearing `Kept` on re-add was tried and dropped: "the
  add succeeded" also means "already in the session", which races a manual delete in progress, and
  the sweep can't tell a re-added torrent from one being removed. Forgetting a Kept placement was
  dropped too: a re-download after the user unblocks would then land as a `[hash8]` copy next to
  the kept files. The cost is one small entry per keep_data delete in `placements.json`.
- **The orphan sweep.** A placement whose torrent is gone, that no record points at and that is
  not `Kept` is a failed torrent whose partial data stayed for a resume
  ([#24](024-a-failed-torrent-is-dropped-from-the-session-and.md)) and whose episode then came
  from another release; the sweep deletes its files. It waits an hour after registration
  (`CreatedAt`, refreshed when a hash registers again), the gap between `SetPlacement` and the add, runs only on the episodes.json
  reloaded after the pass, and `RemovePlacedFiles` refuses a placement not resolved against a
  library.
- **Release keeps the data.** `disable_seeding` removes the torrent and keeps the library file. In
  hardlink mode that file is a hardlink and the download copy can go; here they are the same bytes,
  so `keepData=false` would delete the episode the user just got.
- **Keep forever is a property of the torrent.** The user chose "keep forever" for files written
  in place, which they keep seeding. A torrent added in hardlink mode still has a separate
  download copy and library link and keeps the rules it was downloaded under, so the filter
  checks the record's hash for a placement instead of flipping a global switch — and it is gated
  on `auto_delete_downloads` alone, not on the current `library_mode`: switching back to hardlink
  mode must not start deleting the direct-mode torrents kept until then. The limit lift below is
  the opposite on purpose: `Config.LiftsEpisodeLimit` needs direct mode **on**, so a hardlink
  install keeps `max_episodes_per_anime`. It only filters
  automatic deletion: manual deletes (`RemoveEpisodesWithLinks`, `RemoveTorrentWithEpisodes`) never
  go through it.
- **The limit goes with it.** Under keep forever nothing watched ever leaves, so a per-anime limit
  that also deletes unwatched episodes above it contradicts the choice; the user asked for it off.
  `effectiveMax`/`windowEnd` treat keep forever like `max_episodes_per_anime: 0`, including the open
  pack window. The brakes left are `max_batch_torrent_size_gb` and the disk guard.
- **`DirectLibrary` on the record.** `BackfillShowNFOs` writes the nfo in the folder of
  `LibraryPaths[0]`, which for a direct-mode episode is `Season NN/`, with the entry id. Organize
  already wrote the series nfo with the chain root's id one level up, so marked records are skipped.

**Amendment (see [#141](141-staged-downloads-are-copied-into-the-library-while.md)):** deleting a staging torrent also removes its folder in the incomplete
folder and any `<final>.aad-moving` copy, and cancels a copy into the library still running. A
removal with keep data leaves the staged files with the placement, like library ones — a failure
drop resumes into them, and a manual keep-data delete marks the placement Kept. `Kept` therefore
guards a third path: the incomplete-folder sweep (`pruneStagingLeftovers`) never removes a Kept
placement's staged files; deleting them is the user's call.

**Amendment (see [#152](152-a-library-that-is-a-shoko-drop-folder-releases.md)):** deleting the records of a torrent handed to Shoko never deletes its
files, which are Shoko's. A torrent still seeding leaves as a keep-data delete does: Kept, then
removed with `keepData`. A released one (Shoko moved its files, `releaseMovedByShoko`, or
`disable_seeding` released it) has its placement forgotten instead of marked Kept: the files are
Shoko's, and a Kept, completed placement would resume a later re-add of the hash into files Shoko
may have moved away ([#139](139-a-direct-mode-file-deleted-outside-the-app-is.md)). A re-add gets a
fresh placement, beside a file still there (`[hash8]`).

**Amendment (see [#153](153-a-packs-episodes-already-in-shoko-are-removed-for.md)):** a placed file can be marked `Skipped` (an episode of a pack
already in Shoko, removed). A skipped file missing from disk is no loss: `PlacedFilesMissing` and
the redownload's `missingBytes` leave it out.

**Don't "fix" by:** calling `RemoveFromLibrary` for a placed record "for symmetry"; marking a
placement `Kept` after the removal, or forgetting it instead of marking it (one failed save and
the sweep deletes what the user kept; a forgotten placement re-downloads as a `[hash8]` copy);
clearing `Kept` at all (on re-add or from the sweep, both race a removal in progress) or forgetting
a Kept placement; deleting a Kept placement's files on a removal without a manifest; running the sweep on the start-of-pass snapshot; gating
the deletion filter on `library_mode` — or, the other way, dropping the mode from
`LiftsEpisodeLimit` (it would turn the limit off for every hardlink install); releasing a
placed torrent with `keepData=false`; implementing keep forever as `delete_watched_episodes = false`
(it would also stop pruning hardlink-mode torrents and miss `delete_statuses` and the blacklist);
filtering manual deletes; keeping the episode limit's delete side under keep forever.
