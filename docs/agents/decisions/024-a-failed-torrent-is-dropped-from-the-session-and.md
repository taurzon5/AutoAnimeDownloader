# 24. A failed torrent is dropped from the session and re-added by the next pass — no blacklist

**Location:** `internal/daemon/helpers.go` — `HandleTorrentFailure`; wired as the `onFailed` callback in `cmd/daemon/main.go`.

**What it looks like:** When the embedded client stops a torrent with an error, we fire the `download_failed` webhook and then **remove the torrent from the session** (`Remove`, with the bytes kept when there is progress — see the amendment at the end). Since nothing records that the hash failed, the very next verification pass finds the episode missing from the session and re-adds the same magnet. For a torrent that is genuinely dead (no seeds at all) this is an unbounded re-add loop, one attempt per `check_interval`. That looks like a bug we forgot to guard.

**Why it's right:** The alternative is worse. rain leaves a failed torrent in state Stopped **inside** the session and never restarts it, and the per-torrent listener goroutine exits after `NotifyStop` and is not re-armed (`torrents/session.go`). If we only logged, `episodeInTorrents` (`daemon/helpers.go`) would keep seeing the hash and the daemon would believe the episode was downloaded — forever. No retry, no organize, no notification, and the episode silently never reaches the library. Dropping the torrent converts a permanent silent failure into an automatic retry that reuses the machinery already in place (the loop re-searches Nyaa and may pick a *different*, healthier release the second time). `EpisodeRetryLimit` does not bound this, because it resets each pass.

**Cost of the accepted risk:** a dead torrent costs one Nyaa search plus one magnet add per `check_interval` — cheap, visible in the logs, and each retry fires a `download_failed` webhook so the user is not left guessing.

**Amendment (see [#137](137-direct-mode-deletes-through-the-torrent-and-keep.md)):** the resume rule holds in direct mode, but there the kept partial
data sits in the library (as `.part`, or under its final name with `incomplete_files: in_place`).
When the episode is then downloaded from another release, nothing points at the old hash any
more, and the daemon's orphan sweep (`pruneOrphanPlacements`, end of every pass) deletes those
files and forgets the placement. "Zero verified pieces, nothing to recover" does not hold for a
re-added hash whose placement is `Kept` (the user's files from a keep_data delete): rain removes it
without a manifest, and the placement storage deletes nothing. A placement that is not `Kept` holds
the daemon's own partial data and is cleaned up as this entry describes ([#137](137-direct-mode-deletes-through-the-torrent-and-keep.md)).

**Don't "fix" by:** adding a speculative per-hash blacklist with a TTL. That is the correct fix *if* the re-add churn is ever observed to be a real problem, but it introduces new state to persist, expire, and expose in the UI (otherwise a permanently blacklisted episode becomes the new silent failure). Build it against a real report, not preemptively. Also don't go back to log-only in `onFailed`.

**Amendment (2026-09-07): the re-add RESUMES, it does not start over.** The original version always
passed `keepData=false`, on the grounds that partial data from a torrent that errored was not
reusable. That is false, and on a 90 GiB pack at 90% it cost the entire download for a transient
error. `Session.Add` adds with `AddTorrentOptions{ID: hash}` and the session runs with
`DataDirIncludesTorrentID`, so the re-add lands in the **same** `<DataDir>/<id>`; rain sees the
existing files (`allocator.HasExisting`) and, with no resume-db entry — `RemoveTorrent` deleted it
— runs the verifier and rebuilds the bitfield on its own. Nothing in the app calls `Verify()`: that
method does not exist on rain's public API, and it does not need to.

`keepData` is **conditional**, not a fixed `true`: only with `PiecesHave > 0`. Zero verified pieces
have nothing to recover, and the kept folder would become permanent junk when the retry lands on
another infohash — the directory is per torrent id and nothing sweeps the download path.
`PiecesHave` is the progress source that survives `Stop` (`BytesCompleted` zeroes when rain frees
the pieces), and the failure arrives here already stopped.
