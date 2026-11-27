# 152. A download folder inside one of Shoko's import folders keeps seeding, marks its records handed to Shoko, asks Shoko to import new files, releases a torrent only once Shoko moved its files, and never deletes what it handed over

**Location:**
- `internal/files/filemanager.go`: `Config.ShokoPathMap` (`shoko_path_map`, `ShokoPathMapping`),
  `Config.ShokoPath` / `AppPathFromShoko`, `UnderFolder`, and `EpisodeStruct.HandedToShoko`.
- `internal/shoko/store.go`: `State.ImportFolders`, `Store.ImportFolders` / `SetImportFolders`
  (Shoko's import folders, kept on disk); `client.go`: `ImportNewFiles` and `ImportFolders`.
- `internal/daemon/shoko_import.go`: `ShokoService.ShokoFolder` / `InShoko` (is a download root
  in one of Shoko's import folders), `RequestImport` (one request per settle window),
  `retryImport` (called by `ForPass`), `readFolders`, `ImportState`, and `releaseMovedByShoko`
  (called by the pass after `ForPass`).
- `internal/daemon/jobs.go`: `executeJob` works out `shokoTakesTorrent` before the organize;
  `organizeTorrent(…, toShoko)` marks the records `HandedToShoko`; `processDueJobs` calls
  `ShokoImporter.RequestImport` once per tick that organized such a torrent (`JobQueue.SetShoko`,
  wired in `cmd/daemon/main.go` before `Start`).
- `internal/daemon/episodes.go`: `removeEpisodesAndLinks` never deletes a handed-off torrent's
  files; `shoko_packs.go`'s `dropPackFiles` never deletes a pack's files in one of Shoko's folders.
- `internal/api`: `validateShoko` and `validateShokoPathMap` (`endpoint_config.go`);
  `endpoint_shoko.go` (`import_folders` with `app_path`, `download_roots` with `in_shoko`,
  `last_import_request`, `import_error`); `endpoint_anime_episodes.go` (`handed_to_shoko`); the
  CLI's `shoko status`.
- The settings page: each download folder (the library, when none is listed) says **In Shoko** or
  **Not in Shoko**, Download Folders offers Shoko's import folders to add (`addShokoFolder`), and
  the Shoko group lists them and edits the path map.

Pinned by:
- `TestShokoPathMap` (files), `TestStoreKeepsTheImportFolders` (shoko), `TestShokoFolder`,
  `TestOrganize_ShokoFolderHandsTheTorrentToShoko`, `TestOrganize_OnlyTheShokoFolderHandsOff`,
  `TestOrganize_DisableSeedingStillAsksShokoToImport` and `TestReleaseMovedByShoko`.
- `TestRemoveEpisodes_HandedToShokoKeepsTheFile`, `TestRemoveEpisodes_HandedToShokoPackKeepsEveryFile`,
  `TestRemoveEpisodes_HandedToShokoSeedingKeepsTheFiles` and `TestDropShokoPackFiles` (its Shoko
  folder cases).
- `TestRequestImport` and `TestReadImportFolders` (daemon),
  `TestImportNewFilesAndImportFolders`, `TestImportFoldersOnShoko6` (shoko).
- `TestUpdateConfig_ValidatesShoko`, `TestShokoStatusCarriesShokosFolders`,
  `TestHandleAnimeEpisodes_BatchRange`, `TestShokoStatusPrintsShokosFolders` (CLI), and the
  frontend's `Config.shoko.test.ts` and `AnimeDetail.shoko.test.ts`.

**What it looks like:**
- No setting says which folder goes to Shoko: the app asks Shoko which folders it imports from
  and checks each download folder against them.
- A torrent keeps seeding files Shoko may move, then leaves the client once it has.
- Deleting a handed-off episode in the app leaves its file on disk, and a seeding one's placement
  stays, Kept.
- One `ImportNewFiles` request for many finished downloads, sent a few seconds late.

**Why it's right:**

- **Asked by the user** on 2026-11-24: download into Shoko's folders and, when downloads finish,
  send Shoko a "scan new files" request. For a pack, "wait for shoko to add it and track on our
  side too" ([#149](149-a-pack-chosen-for-the-missing-episodes-records-the.md)). Then: "don't
  disable seeding for in shoko folder option", Shoko's folders picked up "with final say" by the
  user, and "Stop seeding those" for files Shoko moves.
- **Shoko's folders are Shoko's to manage.** The first versions had a setting: one
  `shoko_drop_folder` that made the library the drop folder, then a "Shoko imports from here"
  switch on each download folder once several existed
  ([#154](154-several-download-roots-each-placement-records-its.md)). The user then said: "you use
  the ones from shoko and send the signal to scan for new, not manage the drop folders for shoko,
  only show this folder is in shoko or not" (2026-11-24). So the app reads Shoko's import folders
  (`GET /api/v3/ManagedFolder` on Shoko 6, where import folders became "managed folders" with
  the same fields, else `GET /api/v3/ImportFolder`; with each sync), and a download folder is in Shoko when it is one of
  them or lies inside one. A switch could disagree with Shoko; this can't. Every import folder
  counts, whatever its drop type: those are the folders Shoko scans for new files, and the drop
  type only says where its renamer moves them (not measured per type).
- **A path map, not a guess.** Shoko and the app often see one folder at two paths (one of them in
  Docker). The user chose a path map, like `qbittorrent_path_map`: `shoko_path_map` translates a
  download folder to Shoko's view through the longest matching prefix, and no match keeps the
  path. Recording which Shoko folder a download folder came from would miss folders typed by hand,
  and matching by folder name would guess.
- **The folders are kept on disk.** A torrent that finishes right after a restart, before Shoko is
  read again, must still be handed off, or the app would treat Shoko's file as its own. A failed
  read keeps the last list. When Shoko's folders nest, the deepest one holding a path wins.
- **A folder outside Shoko is an ordinary download folder.** The user downloads some things outside
  Shoko (2026-11-24): no hand-off, no import request, the app's own deletion rules. A torrent is
  handed off by the root its placement is in (`Placement.RootOr`: the library for a placement with
  no root path), and an anime's own folder (#154) can keep it outside Shoko. The settings page
  offers Shoko's folders under Download Folders, the path already translated and editable (never
  one that holds a download folder or lies inside one: download folders can't nest), and says for
  each saved folder whether it is in Shoko; the answer comes from the server, so an edited row
  waits for Save. Added to a list that was empty (the library alone), Shoko's folder goes first and
  the library after it, so new series land in Shoko: the user downloads "with shoko folders only".
  Added to a list, it goes last, and the order is the user's (#154).
- **Seeding goes on.** Shoko imports a file where it lies unless its renamer moves it
  (`Plugins.Renamer.MoveOnImport`, off on a fresh v5.3.3), so most of the time nothing breaks.
- **A torrent Shoko moved leaves.** When Shoko does move the files, the torrent can't seed them.
  rain finds them missing and stops with `ErrPlacedFileMissing`, and qBittorrent reports missing
  files, so the torrent would sit in Downloads as "File not found"
  ([#139](139-a-direct-mode-file-deleted-outside-the-app-is.md)), offering a redownload that would
  only bring Shoko a duplicate. `releaseMovedByShoko` releases it instead: removed with its files
  kept (`releaseTorrent`, as `disable_seeding` releases,
  [#119](119-disable-seeding-means-no-upload-at-all-and-a.md)), records `TorrentReleased`,
  placement forgotten. It acts only when Shoko has one of the torrent's episodes: a file missing
  while Shoko has none of them was deleted by the user, and #139 applies as for any torrent.
- **Shoko never sees a half-written file.** Shoko imports only its `Import.VideoExtensions` (MKV,
  MP4…; measured on v5.3.3), so `.part` names are invisible to it, and rain's staged download
  reaches the library only finished ([#141](141-staged-downloads-are-copied-into-the-library-while.md)).
  While a download folder is in Shoko (as last read), `validateShoko` refuses
  `incomplete_files: "in_place"` without an `incomplete_path`. With qBittorrent it refuses an
  `incomplete_path` too: qBittorrent moves a staged torrent in itself, under the final names, a file
  partly there while it copies ([#145](145-with-qbittorrent-a-staged-torrent-downloads-into.md)).
- **Known limits:** `incomplete_files` is fixed per torrent when it is added, so a torrent added
  under `"in_place"` before its folder was in Shoko still writes its final names. The save check
  knows only the folders last read: a folder Shoko gains later is checked on the next save. And a
  torrent organized before its folder was in Shoko (or before Shoko's folders were first read)
  stays unmarked: deleting its episode deletes its file, which Shoko may have imported since.
- **The records stay.** The episode counts as downloaded, so the pass doesn't search for it again.
  `HandedToShoko` lets the anime page say **Waiting for Shoko** until Shoko has a file for the
  episode, then **In Shoko**.
- **One request for a batch, sent again if it fails.** `ImportNewFiles` queues Shoko's import of
  every new file, so one request covers every download that finished in Shoko's folders in the
  window (`importSettle`, 5 s) and the job-queue tick. It is asynchronous: organize never waits on
  Shoko. A failure (Shoko down, a key without the rights) is shown in the Shoko group, and
  `ForPass` sends it again. Shoko's own folder watcher may be off (`WatchForNewFiles`), so waiting
  for it is not enough. Whether a torrent is Shoko's is worked out before the organize: with
  `disable_seeding` on, organize releases it, and Shoko must still be asked.
- **Removing a pack's episodes in Shoko only skips them here.** An anime set to remove them
  ([#153](153-a-packs-episodes-already-in-shoko-are-removed-for.md)) gets the qBittorrent skip,
  never the delete, for a pack handed to Shoko or in one of Shoko's folders: a file written there
  is Shoko's. The folder counts here, not only the mark, because the pass runs beside the job queue:
  organize finishes the placement (`Completed`) before it saves the records' mark, and a pass in
  between would see a finished pack with unmarked records. Deleting an episode acts on records
  organize already saved, so there the mark alone decides.
- **The app never deletes what it handed over.** The file in Shoko's folder is Shoko's from then
  on, moved or not, and the app never deletes Shoko's files
  ([#147](147-episodes-already-in-shoko-leave-the-list-both.md)). Deleting the episode drops the
  record. A torrent still seeding leaves with `keepData` and its placement marked Kept first, as a
  keep-data delete does ([#137](137-direct-mode-deletes-through-the-torrent-and-keep.md)), so the
  orphan sweep never deletes the files. A released one (Shoko moved its files) has its placement
  forgotten: Kept, a later re-add of the hash would resume into files Shoko moved (#139). The
  hand-off check comes before the placement lookup, so a pack's later records, and a torrent with
  no placement, keep their files too. The record's mark, not the folder, says what was handed over:
  a folder that leaves Shoko later doesn't give those files back to the app.

**Don't "fix" by:**
- Adding a setting or switch that says which folder is Shoko's: the user wants Shoko's own folders
  read, never managed here.
- Handing off every torrent, or asking Shoko to import after any organize, once any folder is in
  Shoko: the user downloads some things outside Shoko, into the other folders.
- Matching folders by name, or only by the exact path: in Docker the paths differ, and the path
  map says how.
- Keeping Shoko's folders in memory only: a torrent finishing after a restart, before the first
  read, would not be handed off.
- Matching a torrent's folder by the anime instead of the placement's root: a torrent is where its
  placement put it, whatever the anime's override says now.
- Working out the hand-off after the organize: with `disable_seeding` on the torrent is gone.
- Stopping seeding at organize: the user wants seeding to go on.
- Releasing a handed-off torrent whose files are missing while Shoko has none of its episodes: the
  user deleted them, and #139 applies.
- Clearing a record or `TorrentReleased` once Shoko has the file: the pass would read the episode
  as never downloaded.
- Deleting a handed-off episode's files, or forgetting a seeding one's placement (the orphan sweep
  deletes the files).
- Checking the hand-off after the placement lookup: a pack's second record would find the
  placement forgotten and delete its files as library hardlinks.
- Calling Shoko from `organizeTorrent` itself, synchronously: a slow Shoko would hold the job queue.
- Allowing `"in_place"` writes into one of Shoko's folders, or qBittorrent's staging.
