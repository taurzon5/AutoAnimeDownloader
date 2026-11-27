# 160. A manual episode download ignores the size and seeder limits, and only the disk guard applies

**Location:** `internal/daemon/manual_download.go` — `ManualDownloadEpisode` hands the ranked
single-episode results to `addAndPrioritize` without `filterSearchResults`; `addAndPrioritize`
reserves disk space (`diskBudget.reserve`) before the add.

**What it looks like:** the scheduled pass (`episodes.go`) and the debug run (`debug.go`) wrap the
same search in `filterSearchResults`, which drops releases over `max_episode_torrent_size_gb` and
under `min_seeders` ([#54](054-seeders-floor-unreadable-seeders-are-discarded.md)). The manual path
doesn't, so it can add a release the pass would refuse. It reads like a forgotten call.

**Why it's right:** the user decided on 2026-11-26 that a manual download is an explicit choice:
someone who clicks Download on one episode wants that episode, even when the only release is above
the size ceiling or has few seeders. The two limits exist to keep the unattended pass from picking
something the user wouldn't, and a click is the user picking. The free-space guard still applies,
because a full disk breaks everything else, not just this download. The results stay ranked, so the
best release is still tried first, and `episode_retry_limit` still caps how many are tried.

**Don't "fix" by:**
- Wrapping the manual search in `filterSearchResults`: a click on an episode whose only releases are
  big or poorly seeded would answer "no torrents found" while Nyaa has them.
- Removing the disk guard from `addAndPrioritize` "for consistency": it is the one limit that
  protects the rest of the library.
