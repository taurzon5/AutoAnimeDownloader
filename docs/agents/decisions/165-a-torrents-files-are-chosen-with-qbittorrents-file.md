# 165. A torrent's files are chosen with qBittorrent's file priorities, and an episode left with no file has no record

**Location:**
- `internal/torrents`: `PlacementRequest.Skip` and `SkipMatcher` (`planFiles` marks the
  `PlacedFile.Skipped` files), `QBitBackend.applyPlacement` (priority 0 before the start, on every
  apply), `TorrentBackend.WantPlacedFiles` (`placementStore.setSkipped` both ways; qBittorrent
  priority 1, rain and the fake only the mark), `Placement.DiskNames`, `ErrFilesNotPlanned`.
- `internal/daemon/file_selection.go`: `CheckFileSelection`/`selectsFiles` (qBittorrent saved and
  running), `PickedRelease.selectedBytes`, `skippedEpisodes` and `withoutSkipForPlacedTorrent`
  for the two picker adds (`ManualDownloadAnimeWithMagnet`, `DownloadStandaloneAnimeWithMagnet`),
  `ChooseTorrentFiles` and `followFileChoice` for a change later, `episodesOfFiles`, `packSkips`,
  which `adoptCoveredEpisodes` (`coverage.go`) consults, and `repicksUnticked`, which the pass's
  episode loop (`withoutUntickedRepicks`), `ManualDownloadEpisode`,
  `ManualDownloadEpisodeWithMagnet` and the episode replace's `CheckEpisodeMagnet`
  (`ErrEpisodeFileUnticked`, asked before the replace deletes anything) consult. `nyaa.TorrentFileList`
  reads the cached list.
- API: `skip_files` on `POST /animes/{id}/replace` and `POST /standalone-animes`,
  `PUT /torrents/{hash}/files`, `skipped` on `GET /torrents/{hash}/files`; CLI `--skip-file`,
  `torrents skip`, `torrents want`; the picker's boxes (`TorrentPicker.svelte`,
  `lib/domain/fileChoice.ts`) and the Downloads file panel (`TorrentFiles.svelte`, `selectable`).

Pinned by `TestPlanFiles_MarksTheUntickedFilesSkipped`,
`TestQBit_PlacementSkipBeforeStartAndWantPutsBack`,
`TestDownloadStandaloneAnimeWithMagnet_FileChoice`,
`TestChooseTorrentFiles_SkipDeletesAndWantBringsBack`, `TestChooseTorrentFiles_GuardsAndRefusals`,
`TestPackSkips_ReadsTheUntickedEpisodes`, `TestCoverageOwnership_UntickedFileIsNotAdopted`,
`TestProcessAnimeEpisodes_LeavesOutTheUntickedTorrent`,
`TestRepicksUntickedAndPlacedTorrentKeepsItsChoice` (with `untickedEpisodes` and
`withoutUntickedRepicks`), `TestCheckEpisodeMagnet`,
`TestQBit_WantingAFileOfAFinishedTorrentReopensIt`, the
API's `torrent_file_choice_test.go`, and the frontend's `fileChoice.test.ts`,
`TorrentFiles.choice.test.ts` and `TorrentPicker.test.ts` ("choosing files").

**What it looks like:**
- Every picker add and every file panel asks the daemon which torrent client is running, and the
  boxes vanish with the built-in one.
- An add whose files were unticked writes fewer episode records than its range names, and the
  coverage adoption, which reads ranges, now reads the placement too.
- Unticking a file that was downloaded deletes it, and its episode's record goes with it, while
  the torrent stays and keeps seeding.

**Why it's right:** this is step 2 of the megapack spec
(`docs/superpowers/specs/2026-11-23-megapack-downloads-design.md`, §4): "you can tick and untick
files and whole entries in the picker before and after the download", decided with the user on
2026-11-23 (rows 2 and 3 of its table).

- **qBittorrent only.** rain v2 has no file selection: it downloads every file of a torrent
  ([#153](153-a-packs-episodes-already-in-shoko-are-removed-for.md)). A choice with it would be a
  lie, so the add and the change refuse with `FILE_SELECTION_UNSUPPORTED` (409), and the boxes
  show only while qBittorrent runs. The check reads the running backend as well as the config: a
  saved `torrent_client` switch only takes effect after a restart. Replace checks before it
  deletes the anime's episodes.
- **Priority 0 before the start.** A placed torrent is added stopped at its metadata (#142), so
  `applyPlacement` sets the skipped files to priority 0 before the recheck and the start:
  qBittorrent never writes them. Set on every apply, which only repeats a value qBittorrent keeps.
- **Paths as Nyaa lists them.** The picker only has Nyaa's file list (cached forever,
  [#163](163-a-megapack-is-a-torrent-whose-files-map-onto-two.md)), which names the torrent's top
  folder like the client does; `SkipMatcher` also matches with the top folder stripped, in case
  the client lays the files out without it.
- **Defaults.** In the picker a megapack starts with everything the mapping places ticked and its
  unmapped videos unticked (the spec's decision 3); any other pack starts with everything ticked.
- **The disk guard counts what downloads.** The pick's size minus the unticked files from the
  cached list (spec §6). Replace used to send size 0 to the guard; it now sends the selected size,
  as the standalone add always sent its size (#93's size trigger).
- **An episode with no file has no record.** A record means the episode is the app's
  ([#147](147-episodes-already-in-shoko-leave-the-list-both.md)): writing one for an unticked
  file would show it downloaded with nothing on disk. The episode of each file comes from the
  megapack mapping (`MapTorrentFiles`, the anime's own entry), else from the number in the file
  name; an episode no file names keeps its record. Ticking a file again brings the record back
  (copied from a record of the same anime on the torrent) and unblocks it.
- **The pass must not undo it.** Ownership by coverage adopts a pending episode a live pack's
  declared range covers ([#78](078-a-torrents-unit-of-ownership-is-coverage-on-the.md)). A pack
  whose range covers an unticked episode doesn't hold it, so `packSkips` reads the placement's
  skipped files, maps them the way organize maps a pack (`nyaa.FilesInPackWindow`,
  [#116](116-restart-per-season-packs-flatten-onto-the.md)), else by the file name's number, and
  the episode is not adopted. The search could also pick the same torrent again for that episode
  (a well-seeded pack usually ranks first): `repicksUnticked` leaves such a candidate out, in the
  pass and in a manual episode download, since the add would keep the torrent's placement and
  record the episode with no file. A pack candidate is left out when any of the anime's episodes
  is unticked in it. In the pass it is left out before the attempt: an episode a pack was
  resolved for falls back to a search of its own as a single episode (the pack's folder rule,
  name and range would lie on another torrent's record, #78), and an episode with nothing but such
  torrents is no issue at all (no `download_failed`,
  [#128](128-download-failed-reports-a-real-failure-once-per.md), no `episode_stuck` two days on)
  and no hold on the later episodes
  ([#100](100-an-empty-per-episode-search-waits-before-repeating.md)): the user unticked it. A pasted
  magnet of it answers 409 `EPISODE_FILE_UNTICKED`.
- **A torrent the app already has keeps its choice.** Its placement keeps its first plan
  ([#136](136-direct-mode-places-a-torrent-before-the-add-and.md)), so a pick of it drops the
  pick's choice, and the placement's own unticked files say which episodes get no record
  (`untickedEpisodes`): marking files of a pack another cour holds would take them from that cour.
  Its files change from Downloads.
- **Ticking a file again on a finished torrent reopens it**, as a redownload of missing files does
  ([#139](139-a-direct-mode-file-deleted-outside-the-app-is.md)): the download path is cleared, the
  file is renamed ".part" before it gets priority 1, and the placement is not `Completed` until the
  finish step renames it; the `reopened` mark keeps the old complete state from finishing it early
  ([#140](140-redownloading-missing-files-adds-the-same-torrent.md)). Otherwise it would read as
  "File not found" while it downloads, and be
  written under the final name Jellyfin and Shoko read
  ([#152](152-a-library-that-is-a-shoko-drop-folder-releases.md): Shoko never sees a half-written
  file).
- **Unticking a written file deletes it.** "Deleting a watched season unticks its files: they leave
  the disk and stop seeding" (spec §4). qBittorrent keeps seeding the rest after a priority-0 file
  is deleted (measured on 5.x, #153). The UI asks first. A finished file in one of Shoko's import
  folders, or of a torrent handed to Shoko, is Shoko's and stays (`kept`,
  [#152](152-a-library-that-is-a-shoko-drop-folder-releases.md)); a `.part` or `.!qB` file there
  is deleted, since Shoko never imports one (measured: its video extensions exclude `.part`). An
  episode whose file is still on disk (kept now or by an earlier change) still has it, so its
  record stays.
- **Block is optional.** Without it, an episode left with no file is missing again, and the pass
  may download it from another torrent. With it (`--block`, the confirm's box), it is blocked like
  deleting a torrent with block. Proposed default; the user hasn't chosen yet.

Trade-offs accepted:
- The disk guard of a pick counts the size Nyaa gives minus the unticked files; for a torrent the
  app already has it counts the whole torrent, as before.
- The mapping of files to episodes is a best effort without AniList or a numbered name: such an
  episode keeps its record and the adoption doesn't see the skip.

**Don't "fix" by:**
- Recording a skip with rain "to keep the UI the same": rain downloads the file anyway, and the
  record would say the file isn't there.
- Writing the records for every episode in the pack's range, as before: the anime page would show
  unticked episodes as downloaded, and the pass would never fetch them.
- Dropping the `packSkips` check from the adoption: the next pass would write the records back,
  pointing at files that were never downloaded.
- Skipping in the pass now: the pass still takes whole packs, and the season-range hold
  ([#83](083-the-pack-names-range-is-the-first-plausible-match.md)) stays until the pass chooses
  files itself (spec step 3, with "Download related seasons").
