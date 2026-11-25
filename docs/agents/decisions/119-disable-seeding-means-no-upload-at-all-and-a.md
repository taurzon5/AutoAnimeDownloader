# 119. `disable_seeding` means no upload at all, and a released torrent is not a lost one

**Location:** `internal/torrents/session.go` — `sessionOptions.noUpload`, `rainConfig`;
`internal/torrents/sessionmanager.go` — `SetUploadDisabled`, the rebuild in `ensure`;
`internal/daemon/jobs.go` — `releaseTorrent`; `files.EpisodeStruct.TorrentReleased`, read in
`selectEpisodes`, `removeEpisodesAndLinks` and `clearLibraryPathsAfterRootSwap`;
`reconcileLibrary(…, disableSeeding)`. Pinned by `TestRainConfigNoUploadClosesEveryUploadPath`,
`TestSessionManagerEnsureRebuildsWhenUploadSettingChanges`,
`TestOrganizeTorrent_DisableSeedingReleasesTheTorrent`,
`TestDisableSeeding_ReleasesTorrentsOrganizedBefore`,
`TestSelectEpisodes_ReleasedTorrentIsNotMissing` and the rest of `daemon/disable_seeding_test.go`.

**What it looks like:** a "disable seeding" switch that also changes how the torrent DOWNLOADS,
rebuilds the whole rain session to apply, and a new flag on every record instead of just
removing the torrent after organize.

**Why it's right:** the request (issue #40) is "don't upload", for legal reasons. Removing the
torrent after organize stops seeding but not uploading: BitTorrent uploads to peers WHILE it
downloads. rain has no "upload off": its upload speed limit treats 0 as unlimited. What works is
never unchoking a peer (`UnchokedPeers` and `OptimisticUnchokedPeers` 0) **and** an empty BEP 6
allowed-fast set (`AllowedFastSet` 0). The allowed-fast set is the pieces a peer may request even
while choked, and rain serves 10 of them by default. With only the unchokes at zero, every peer
still got those 10. rain reads all three once, when the session opens, so changing the setting
rebuilds the session. The cost: peers favor whoever uploads back, so downloads can be slower.

**The flag on the record:** for the pass, a record whose torrent is missing from the session is a
lost download, and it fetches it again ("Redownloading episode: was missing from torrents"). A
released torrent is missing on purpose. Without `TorrentReleased`, every organized episode would be
downloaded again on the next pass. The flag is written BEFORE the removal: a crash in between leaves
a flagged record whose torrent is still there, which the next reconciliation removes. The other order
would leave an unflagged record with no torrent, which would be downloaded again. It stays set if the
setting is turned off later, since the file is still in the library. It is cleared only by a root swap,
because then the library it lived in is gone.

**A multi-season pack goes right away**, unclaimed holds included ([#117](117-off-list-bypasses-the-unclaimed-pack-guard.md)):
whoever turns seeding off doesn't want data parked on disk. If S2 enters watching later it downloads
again. `findCoveringPack` does not read the flag on purpose, since it adopts what is ON DISK and a
released pack is not.

**Amendment (see [#137](137-direct-mode-deletes-through-the-torrent-and-keep.md)):** for a direct-mode torrent the release removes the torrent with
`keepData=true`: its files are the library, there is no download copy to delete. The upload side
(no unchoke, no allowed-fast set) is unchanged. Because nothing is deleted, a released direct-mode pack keeps its other
seasons and extras on disk, out of the session: a later season cannot adopt it (`findCoveringPack`
needs a live torrent, [#78](078-a-torrents-unit-of-ownership-is-coverage-on-the.md)) and downloads its own copy. The "parts that were not linked are deleted"
rule above does not apply to direct mode.

**Don't "fix" by:** using `SpeedLimitUpload` (0 = unlimited); zeroing the unchokes and leaving
`AllowedFastSet`; also stopping the torrent at completion: organize runs right after completion and
removes it, and with no upload a completed torrent has nothing to send anyway, so a stop in between
only adds a state to keep in sync; treating `TorrentReleased` as "in session" in `findCoveringPack`.

**Amendment (see [#142](142-qbittorrent-stops-each-placed-torrent-at-its.md)):** qBittorrent can't refuse every upload (an upload limit of 0
means unlimited there), so the qBittorrent backend's step 1 **refused** `disable_seeding` with
`torrent_client: "qbittorrent"` rather than silently uploading. Superseded by the next amendment.

**Amendment (see [#144](144-with-qbittorrent-the-apps-torrents-never-have-a.md)):** with qBittorrent, `disable_seeding` is accepted and means
**almost no upload**: every torrent of the app is held at `qbittorrent_disable_seeding_upload_kib`
(16 KiB/s by default) while it downloads, re-applied on every poll, and released once organized,
as above (`releaseTorrent` keeps its files in direct mode). Nothing smaller works: the limit also
carries the download's own requests, and 1 B/s stalled a download before it had its file list.
qBittorrent also saves 0 upload slots as unlimited. The no-unchoke, no-allowed-fast rule above
stays rain's.

**Amendment (see [#152](152-a-library-that-is-a-shoko-drop-folder-releases.md) and [#153](153-a-packs-episodes-already-in-shoko-are-removed-for.md)):** `releaseTorrent` has two more callers, neither
tied to `disable_seeding` nor to `reconcileLibrary`: `releaseMovedByShoko`, for a torrent handed
to Shoko whose files Shoko moved (`movedByShoko` keeps the records marked `HandedToShoko`), and
`dropPackFiles`, for a rain pack whose episodes already in Shoko are deleted.
