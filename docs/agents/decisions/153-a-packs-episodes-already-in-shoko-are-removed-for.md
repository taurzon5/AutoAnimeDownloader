# 153. A pack's episodes already in Shoko are removed for an anime that doesn't keep them: skipped with qBittorrent, deleted once downloaded otherwise, and a rain pack stops seeding

**Location:**
- `internal/files/filemanager.go`: `Config.KeepsShokoPackEpisodes` (the anime's
  `AnimeSettings.ShokoPackEpisodes`, `download` or `remove`, else `shoko_full_packs`).
- `internal/daemon/shoko_packs.go`: `dropShokoPackFiles` (called by the pass after
  `ShokoService.ForPass`), `unneededPackFiles` and `dropPackFiles`.
- `internal/torrents`: `PlacedFile.Skipped`, `TorrentBackend.SkipPlacedFiles` (qBittorrent:
  `torrents/filePrio` priority 0 by file index; rain: the mark only), `placementStore.setSkipped`.
  `Placement.Resolve` keeps every field of a file. Skipped files are left out of `VideoPaths`,
  `finalizeFiles`, qBittorrent's `FinalizePlacement` and `finalizeStaged` waits, and the daemon's
  `PlacedFilesMissing` and `missingBytes`.
- `internal/api/endpoint_anime_settings.go` (`shoko_pack_episodes`: `download`, `remove`,
  `default`), `endpoint_anime_episodes.go` (`pack_episodes`, `keep_pack_episodes`,
  `keep_pack_episodes_default`), the CLI's `animes settings <id> shoko-packs`, and
  `ShokoMatchPanel.svelte`.

Pinned by:
- `TestDropShokoPackFiles` (rain releases, qBittorrent keeps seeding, kept/recorded/unfinished).
- `TestQBit_SkipPlacedFilesSetsPriorityZero` and `TestPlacedStorage_OpenRecreatesASkippedFile`.
- `TestAnimeSettingsShokoPackEpisodes`, `TestAnimesSettingsShokoPacks`, and the frontend's
  `ShokoMatchPanel.test.ts`.

**What it looks like:**
- The app deletes files of a torrent it downloaded on purpose, by itself.
- The same setting skips a file with one client and deletes it with the other.
- With rain, removing a few episodes stops the whole pack seeding.
- A pack added between two checks may download those files anyway, and they go at the next check.

**Why it's right:**

- **Asked by the user** (2026-11-24): a per-anime override "to download or remove the unneeded
  episodes", and, asked how: "skip, if not downloaded, otherwise delete them". The global
  Download Full Megapacks switch is the default for both: on keeps them
  ([#149](149-a-pack-chosen-for-the-missing-episodes-records-the.md)), off removes them.
- **Unneeded means in Shoko and not the app's.** An episode the app has a record of is its own copy
  ([#147](147-episodes-already-in-shoko-leave-the-list-both.md)), so it is never removed, whatever
  Shoko has.
- **Which file is which episode** comes from `nyaa.FilesInPackWindow` over the torrent's own paths,
  with the record's `SeriesOffset`, `AnimeSeason` and `AnimeTotalEpisodes`, the mapping organize
  already uses for packs ([#116](116-restart-per-season-packs-flatten-onto-the.md)). Without an
  episode total there is no window, and nothing is removed: a wrong guess would delete an episode
  the user wanted. Extras, unnumbered files and another cour's files are never touched.
- **qBittorrent skips.** Priority 0 before the data comes means the file is never written, and
  deleting a priority-0 file that was written doesn't stop the torrent seeding the rest, even after
  a recheck. Both measured on qBittorrent 5.x with the live test containers
  (`scripts/qbittorrent-live.sh`). A placed torrent's file `i` is qBittorrent's index `i`
  (`applyPlacement` plans them in index order).
- **rain can't.** rain v2 has no file selection (the megapack spec), so it downloads the whole
  pack. Once it finished, the files are deleted. A finished rain torrent with a file missing stops
  with `ErrPlacedFileMissing` ([#139](139-a-direct-mode-file-deleted-outside-the-app-is.md)), so
  it is released first (`releaseTorrent`, the rest of its files kept): the pack stops seeding.
  That trade-off was in the question the user answered.
- **A skipped file is no loss.** Missing, it doesn't make the torrent read as "File not found",
  isn't counted by "Redownload missing files", and doesn't hold up qBittorrent's finish step, which
  waits for every file to have its size. It isn't a library path either: the records' paths drop
  the deleted files.
- **Once per check, not at the metadata.** The pass knows Shoko's episodes and the anime's choice;
  the backend's metadata step knows neither, and the pack's records may not be saved yet when the
  metadata comes. A pack downloads for longer than a check interval almost always, so the skip
  usually lands before the files are written; when it doesn't, the files are deleted at the next
  check ("otherwise delete them").
- **Handed to Shoko, only skipped.** In one of Shoko's import folders, a file written there is
  Shoko's, and the app never deletes Shoko's files
  ([#152](152-a-library-that-is-a-shoko-drop-folder-releases.md)). So for a pack handed to Shoko,
  or one in such a folder (records marked or not), removing them is the skip alone: qBittorrent
  never writes them, and what rain wrote stays for Shoko.
- **A re-added finished pack writes a skipped file again.** `Open` refuses to recreate a finished
  torrent's missing file (#139), but a skipped file is one the daemon deleted, and
  `PlacedFilesMissing` doesn't count it. Refusing it would stop the torrent on an error the failure
  handler doesn't recognize, and it would drop and re-add the torrent. Written again, it is deleted
  again once the torrent finished. #139 and #22 ([#22](022-organize-everything-to-completed-anime-path-and.md))
  are amended.
- **`Resolve` keeps every field.** It rebuilt each file from `Torrent` and `Path` only, which
  dropped `Skipped` from every placement the backends hand back.

**Don't "fix" by:**
- Removing an episode the app has a record of, or one no window maps (no total, an extra, another
  cour): that deletes what the user wanted.
- Deleting a rain torrent's files while it is still in the client: it stops with a missing file
  and shows "File not found".
- Counting skipped files in `PlacedFilesMissing`, `missingBytes` or qBittorrent's finish waits.
- Rebuilding `PlacedFile` field by field in `Resolve`.
- Skipping in the backend at the metadata without the anime's choice and Shoko's episodes.
- Deleting the files of a pack handed to Shoko.
- Making `Open` refuse a skipped file of a finished placement again.
