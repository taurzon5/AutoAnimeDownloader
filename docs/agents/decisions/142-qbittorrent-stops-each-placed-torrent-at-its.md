# 142. qBittorrent stops each placed torrent at its metadata and the app renames the files before starting it

**Location:** `internal/torrents/qbittorrent.go` — `QBitBackend.Add` (`StopAtMetadata`, the library
root as `savepath`, `awaitingStart`), `applyPlacement` (`planFiles` over qBittorrent's file list,
`renameFile`, recheck and start), `Remove` (`deleteFiles=false` for a placed torrent, then
`RemovePlacedFiles`), `eventsLocked` (completion and failure by polling `sync/maindata`),
`FinalizePlacement`; `internal/torrents/qbit_client.go`. Pinned by
`TestQBit_PlacedTorrentIsRenamedBeforeItStarts`, `TestQBit_PauseWhileAwaitingRenamesStaysStopped`,
`TestQBit_RemoveDeletesPlacedFilesItself`, `TestQBit_TorrentFirstSeenCompleteFiresNothing`,
`TestQBit_MissingFilesOfAFinishedPlacement`, `TestQBit_RedownloadRechecksAndStarts`.

**What it looks like:** qBittorrent has its own save paths, content layouts and categories with
folders. Adding each torrent with `savepath` set to its `Season NN/` folder and
`contentLayout=NoSubfolder` looks like the natural way to get direct mode's layout. Instead the
backend adds every placed torrent at the **library root**, stopped as soon as its file list is
known (`stopCondition=MetadataReceived`), renames each file to the path `planFiles` gave it, and
only then starts it. And removing a placed torrent asks qBittorrent to drop the torrent but **not**
its files: the app deletes them itself.

**Why it's right:**

- **One placement rule for both clients.** Direct mode's layout, the Jellyfin rename, the collision
  suffix, the frozen file map and `placements.json` are the app's (#136). With the library root as
  the save path, qBittorrent's file paths are library-relative paths, and a rename per file applies
  the exact map rain's storage applies. Recreating the layout through save paths and content
  layouts would be a second, diverging implementation of the same rule (and cannot express a
  renamed single file in a pack, or the `[hash8]` suffix).
- **Nothing is written under the torrent's own names.** Started right away, qBittorrent would begin
  writing pieces under the raw names before the renames land. Stopping at the metadata makes the
  renames happen on empty files. A torrent the user pauses meanwhile is not started afterwards;
  one resumed before its renames starts once they are done (`awaitingStart`).
- **The app deletes placed files with its own rule.** `RemovePlacedFiles` also removes the `.part`
  and `.nfo` names and prunes the folders left empty up to the library root, and respects Kept
  (#137). qBittorrent's `deleteFiles` knows none of that.
- **Completion and failure come from one poll of `sync/maindata`** (qBittorrent has no push to the
  app). A torrent first seen already complete fires nothing: the verification pass's reconcile
  handles it, as with rain. `missingFiles` on a finished placement is reported as
  `ErrPlacedFileMissing`, the same error rain gives for files deleted outside the app (#139).

**What the daemon reads, kept rain's way.** `torrents/info` has no piece counts, so progress on
a fixed scale stands in for `PiecesHave`/`PiecesTotal` (`qbitProgressScale`): the failure handler
keeps a torrent's data when it has any ([#24](024-a-failed-torrent-is-dropped-from-the-session-and.md)),
and a failed pack at 90% must not lose it. A torrent removed before qBittorrent had its metadata
keeps a Kept placement's files even when an earlier run froze the map (#137). The download-root
markers are checked on every `Ensure`, not only on reconnect
([#34](034-a-download-folder-swap-is-detected-by-a-dual.md)). A redownload waits for qBittorrent's
recheck before a completion counts ([#140](140-redownloading-missing-files-adds-the-same-torrent.md)).
Pinned by `TestQBit_ProgressStandsInForPieces`, `TestQBit_RemoveKeptBeforeMetadataKeepsFiles`,
`TestQBit_LibrarySwapIsReportedOnTheNextPass`.

**What a real qBittorrent changed.** The first run against qBittorrent 5.3.0 (Web API 2.16.2,
`TestQBitLive`, [testing.md](../testing.md#qbittorrent-live-test)) failed in four places that the
fake had answered the old way:

- **Every add sends `useDownloadPath=false`**, except a staged placement, which gets its own
  `<incomplete_path>/<hash>` as its download path
  ([#145](145-with-qbittorrent-a-staged-torrent-downloads-into.md)). With qBittorrent's global "Keep incomplete torrents
  in" on, a torrent downloads into that folder and is moved at the end. The renames would then
  apply under a folder the app never looks at. In the official image that folder was
  `/downloads/temp`, and the download stopped with "permission denied". The setting is per torrent,
  so the user's own torrents keep it. Pinned by `TestQBit_PlacedTorrentIsRenamedBeforeItStarts`.
- **Success is any 2xx, and an add reports its own result.** Web API 2.11.8+ answers success with
  an empty 204, a good login included. Since 2.14 a refused login answers 401 instead of 200
  `Fails.`, and `torrents/add` answers JSON, with 202 while the add is pending and 409 when every
  torrent fails. `login`, `call` and `qbitAddResult` read both formats. Pinned by
  `TestQBit_ModernResponseFormats`, `TestQBitAddResult`.
- **A refused login names the Docker port.** qBittorrent's Host header validation answers 401 when
  the request's port is not its WebUI port, which is what a Docker mapping like `-p 8080:8090` gives.
  The right password then looks wrong, so `errQBitLoginRefused` says to use the same port on both
  sides or to turn the validation off.
- **The connection test's probe has no leading dot.** `getDirectoryContent` hides dotfiles, so a
  `.aad-…` probe was never seen and every library looked invisible to qBittorrent. Pinned by
  `TestTestQBittorrent_ProbesTheLibrary` (the fake hides dotfiles too).

**Don't "fix" by:** leaving `PiecesHave` at 0 "because qBittorrent has no pieces" (every failure
then deletes the data); adding placed torrents with a per-episode `savepath` or `NoSubfolder`;
starting a placed torrent before its renames (or dropping `stopCondition`); sending
`deleteFiles=true` for a placed torrent; firing `onComplete` for a torrent first seen complete;
mapping `missingFiles` to a generic error; dropping `useDownloadPath=false` "because the default is
off"; requiring 200 `Ok.` from a login or 200 from an add; giving the probe a hidden name.
