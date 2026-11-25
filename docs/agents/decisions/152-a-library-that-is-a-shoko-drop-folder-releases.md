# 152. A library that is a Shoko drop folder keeps seeding, marks its records handed to Shoko, asks Shoko to import new files, releases a torrent only once Shoko moved its files, and never deletes what it handed over

**Location:**
- `internal/files/filemanager.go`: `Config.ShokoDropFolder` (`shoko_drop_folder`),
  `Config.HandsOffToShoko` (Shoko on, the setting on, direct mode) and
  `EpisodeStruct.HandedToShoko`.
- `internal/daemon/jobs.go`: `organizeTorrent` marks the records `HandedToShoko`;
  `JobQueue.processDueJobs` calls `ShokoImporter.RequestImport` once per tick that organized one
  (`JobQueue.SetShoko`, wired in `cmd/daemon/main.go` before `Start`).
- `internal/daemon/shoko_import.go`: `RequestImport` (one request per settle window),
  `retryImport` (called by `ForPass`), `readFolders`, `ImportState`, and `releaseMovedByShoko`
  (called by the pass after `ForPass`).
- `internal/shoko/client.go`: `ImportNewFiles` (`GET /api/v3/Action/ImportNewFiles`) and
  `ImportFolders`.
- `internal/daemon/episodes.go`: `removeEpisodesAndLinks` never deletes a handed-off torrent's
  files.
- `internal/api/endpoint_config.go`: `validateShoko`; `endpoint_shoko.go` (`import_folders`,
  `last_import_request`, `import_error`); `endpoint_anime_episodes.go` (`handed_to_shoko`).
- The settings page's Shoko group: the drop folder is a suggestion (Shoko's drop sources, the path
  filled in and editable, "Use This Folder", then Save), not a bare switch.

Pinned by:
- `TestOrganize_DropFolderHandsTheTorrentToShoko` and `TestReleaseMovedByShoko`.
- `TestRemoveEpisodes_HandedToShokoKeepsTheFile`, `TestRemoveEpisodes_HandedToShokoPackKeepsEveryFile`
  and `TestRemoveEpisodes_HandedToShokoSeedingKeepsTheFiles`.
- `TestRequestImport` and `TestReadImportFolders` (daemon),
  `TestImportNewFilesAndImportFolders` (shoko).
- `TestUpdateConfig_ValidatesShoko`, `TestShokoStatusCarriesTheDropFolder`,
  `TestHandleAnimeEpisodes_BatchRange`, `TestShokoStatusPrintsTheDropFolder` (CLI), and the
  frontend's `Config.shoko.test.ts` and `AnimeDetail.shoko.test.ts`.

**What it looks like:**
- There is no drop folder path: the library path itself is the drop folder.
- A torrent keeps seeding files Shoko may move, then leaves the client once it has.
- Deleting a handed-off episode in the app leaves its file on disk, and a seeding one's placement
  stays, Kept.
- One `ImportNewFiles` request for many finished downloads, sent a few seconds late.

**Why it's right:**

- **Asked by the user** on 2026-11-24: download into Shoko's drop folder and, when downloads
  finish, send Shoko a "scan new files" request. For a pack, "wait for shoko to add it and track on
  our side too" ([#149](149-a-pack-chosen-for-the-missing-episodes-records-the.md)). Then: "don't
  disable seeding for in shoko folder option", the drop folder "should act as a strong suggestion
  rather than option", with Shoko's folders offered and the user's final say, and "Stop seeding
  those" for files Shoko moves.
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
- **The library is the drop folder.** Direct mode writes to one root, and several roots are the
  direct-library-mode spec's PR 2, on its own branch (the user's call); a "Shoko imports from here"
  switch per folder comes with it. The user points `completed_anime_path` at the folder Shoko
  imports from. The Shoko group suggests it: it lists Shoko's import folders as Shoko sees them (in
  Docker, inside its container), fills in a drop source's path, and lets the user change it, since
  the app may see that folder at another path. The app can't match those paths to its own, so it
  doesn't check them.
- **Shoko never sees a half-written file.** Shoko imports only its `Import.VideoExtensions` (MKV,
  MP4…; measured on v5.3.3), so `.part` names are invisible to it, and rain's staged download
  reaches the library only finished ([#141](141-staged-downloads-are-copied-into-the-library-while.md)).
  `validateShoko` refuses `incomplete_files: "in_place"` without an `incomplete_path`. With
  qBittorrent it refuses an `incomplete_path` too: qBittorrent moves a staged torrent in itself,
  under the final names, a file partly there while it copies
  ([#145](145-with-qbittorrent-a-staged-torrent-downloads-into.md)).
- **Known limit:** `incomplete_files` is fixed per torrent when it is added. A torrent added under
  `"in_place"` before the drop folder was turned on still writes its final names, so turn the
  setting on when no such download is running.
- **The records stay.** The episode counts as downloaded, so the pass doesn't search for it again.
  `HandedToShoko` lets the anime page say **Waiting for Shoko** until Shoko has a file for the
  episode, then **In Shoko**.
- **One request for a batch, sent again if it fails.** `ImportNewFiles` queues Shoko's import of
  every new file, so one request covers every download that finished in the window
  (`importSettle`, 5 s) and the job-queue tick. It is asynchronous: organize never waits on Shoko.
  A failure (Shoko down, a key without the rights) is shown in the Shoko group, and `ForPass` sends
  it again. Shoko's own folder watcher may be off (`WatchForNewFiles`), so waiting for it is not
  enough.
- **Removing a pack's episodes in Shoko only skips them here.** An anime set to remove them
  ([#153](153-a-packs-episodes-already-in-shoko-are-removed-for.md)) gets the qBittorrent skip, never the delete: a file written into the drop folder is
  Shoko's.
- **The app never deletes what it handed over.** The file in the drop folder is Shoko's from then
  on, moved or not, and the app never deletes Shoko's files
  ([#147](147-episodes-already-in-shoko-leave-the-list-both.md)). Deleting the episode drops the
  record. A torrent still seeding leaves with `keepData` and its placement marked Kept first, as a
  keep-data delete does ([#137](137-direct-mode-deletes-through-the-torrent-and-keep.md)), so the
  orphan sweep never deletes the files. A released one (Shoko moved its files) has its placement
  forgotten: Kept, a later re-add of the hash would resume into files Shoko moved (#139). The
  hand-off check comes before the placement lookup, so a pack's later records, and a torrent with
  no placement, keep their files too.

**Don't "fix" by:**
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
- Allowing `"in_place"` writes into the drop folder, or qBittorrent's staging.
