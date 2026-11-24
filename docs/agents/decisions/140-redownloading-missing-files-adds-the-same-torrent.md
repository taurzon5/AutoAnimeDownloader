# 140. Redownloading missing files adds the same torrent back to rain instead of restarting it

**Location:** `internal/torrents/session.go` — `Session.RedownloadPlacement` (remove with
keep-data, `placementStore.reopen`, `AddTorrent` with the torrent's own `.torrent` bytes) and the
`Seeding`/`Stopped` check in `Session.FinalizePlacement`; `internal/torrents/sessionmanager.go` —
`SessionManager.RedownloadPlacement` (the queue's enforce after each add);
`internal/daemon/placement.go` — `RedownloadMissingFiles`. Pinned by `TestDirectMode_RedownloadPlacementFetchesTheDeletedFileAgain`
(real rain session, with a restart before the first piece),
`TestSessionManager_RedownloadPlacementRespectsTheQueue`,
`TestRedownloadMissingFiles_PicksTheTorrentsWithMissingFiles`,
`TestRedownloadMissingFiles_SkipsWhatDoesNotFit` and `TestHandleTorrentsRedownloadMissing`.

**What it looks like:** to fetch a deleted file again, the torrent is removed from rain and the
same torrent is added back, losing its added time and upload stats. A plain `Start()` after
clearing `Completed`, or rain's own `Verify()`, looks like the obvious, cheaper way.

**Why it's right:** the bulk "Redownload missing files" is the user's way out of
[#139](139-a-direct-mode-file-deleted-outside-the-app-is.md) (POST
`/api/v1/torrents/redownload-missing`, CLI `torrents redownload-missing`, and a dialog in Downloads
listing every torrent with missing files, all ticked, where the user unticks the ones to leave
alone). Both obvious ways go wrong in rain v2.5.1, and a real-session test caught each:

- **Restart:** rain keeps the old, complete bitfield in its resume db and its in-memory
  "completed" state across a stop. The restarted torrent showed **Seeding with 0 pieces** and never
  downloaded. Even where it does start downloading, a stop before the first piece arrives (no peers
  yet, a daemon restart) leaves an empty `.part` that the next start finds "existing", so it
  trusts the stale bitfield and the empty file passes as finished: zeros in the library.
- **`Verify()`:** it deletes the stored bitfield, but stops the torrent after the check. It also
  only recreates the completion channel when it is nil, never when it is already closed. That
  leaves the closed channel in place: the listener would fire organize at once, and rain would
  close the same channel again when the download finishes.

A torrent added fresh has no stored bitfield and a new completion channel. It verifies what is on
disk, reuses the files still there (the crash fallback in `placedStorage.Open` finds them under
their final names) and fetches only what is gone. The placement survives the removal because
`placedStorage.Remove` keeps everything with keep-data. Lost added time and upload stats are the
price.

**The queue still holds them.** rain starts a torrent as it is added, so
`SessionManager.RedownloadPlacement` runs the queue's enforce afterwards, as `Add` does: a replaced
library folder can mean hundreds of torrents in one click, and they must wait for
`max_concurrent_downloads` slots ([#41](041-the-download-queue-holds-every-incomplete-torrent.md)).
Enforce runs after **each** add, not once after the bulk loop: once at the end would let every
torrent of the batch allocate and verify at the same time while the loop runs. The price is
accepted: each enforce lists the session, one `Stats()` per torrent
([#30](030-progress-data-comes-from-one-stats-per-torrent.md)), so N redownloads in a session of M
torrents cost about N×M calls — a one-off click, not a poll. The CLI command has no `--yes`:
nothing is deleted ([#104](104-yes-is-a-cli-gate-the-api-keeps-its-query-defaults.md)).

**Finalize waits for a settled torrent.** Right after a start, rain reports the old bitfield while
it allocates and verifies. `FinalizePlacement` therefore requires `Seeding` or `Stopped` on top of a
complete bitfield; otherwise a still-empty `.part` could get its final name. Organize retries.

**The disk guard counts what the session still has to write**
([#101](101-the-disk-guard-counts-what-the-queue-still-has-to.md)), plus each started torrent's
missing bytes. A torrent that doesn't fit is skipped, keeps its `Completed` placement (nothing
recreates its file) and is counted in `skipped_no_space`.

**Amendment (see [#141](141-staged-downloads-are-copied-into-the-library-while.md)):** `reopen` also takes the incomplete folder configured now: files still in
the library are reused in place, the missing ones stage and move like any download.

**Don't "fix" by:** going back to `Start()` or `Verify()` on the same torrent; dropping the
queue's enforce after the add; dropping the
`Seeding`/`Stopped` check in `FinalizePlacement`; letting the bulk action run on its own (a pass,
the swap check). It is the user's click, and #139's "nothing else" still holds otherwise.

**Amendment (see [#142](142-qbittorrent-stops-each-placed-torrent-at-its.md)):** the rules above are rain's: rain trusts its stale bitfield, so
`Start`/`Verify` can't make it look again. qBittorrent's recheck does look again, so
`QBitBackend.RedownloadPlacement` reopens the placement and the poll loop renames the missing files
to `.part`, rechecks and starts — no remove and re-add. What carries over is the "settled" rule:
until qBittorrent shows it took the recheck (`reopened`), the torrent's old complete state neither
fires `onComplete` nor lets `FinalizePlacement` rename anything; `FinalizePlacement` also waits for
a complete torrent that isn't checking or moving.
