# 164. Download queues search in their order and rank with their own priorities, live in their own store, and share the download limit

**Location:** `internal/files/queues.go` — `QueueStore`, `DownloadQueue`, `QueueOf`, `MoveAnime`,
`withDefaultQueue`; `internal/daemon/queues.go` — `queueGroups`, `queueRanking`, the package-level
`queueStore`; `internal/daemon/verification.go` — the pass's loop over `queueGroups` with an
`animeWg.Wait()` after each group; `internal/daemon/search.go` — `nyaaSearcher.ranking`,
`rankSeriesResults`, `rankMovieResults`; `internal/nyaa/priorities.go` and `nyaa.go` —
`SortTorrentResultsBy`, `SortFinishedSeriesResultsBy`, `SortMovieResultsBy` (comparators take the
ranking); `internal/api/endpoint_queues.go`. Tests: `TestQueueStore_*`, `TestQueueOfAndMoveAnime`,
`TestQueueGroups_FollowTheQueueOrder`, `TestQueueRanking_SortsWithTheQueuesPriorities`,
`TestQueues_*` (API), `TestQueuesCommandsHitTheirEndpoints`, the Priorities, Add anime and
`AnimeQueuePicker` component tests.

**What it looks like:**
- `queues.json` next to `config.json`, when every other setting is a `config.json` field that
  `config set` can reach;
- the default queue lists no anime and has no ranking of its own in the file;
- the pass waits for every anime of one queue before starting the next, which slows the pass down
  when the first queue has one slow anime;
- downloads ignore the queue: a later queue's torrent can start before an earlier queue's;
- results are sorted twice: once by the scrape with the active ranking, once more by the queue's;
- a queue's saved ranking carries an ignore list nothing reads.

**Why it's right:** the user asked on 2026-11-25 for queues made by them, with an order and a
ranking each, picked when adding an anime, and decided on 2026-11-26: **one shared download
limit**, and **the order holds back searching, not downloading** (recorded in the backlog
note that this change deleted).

- **Searching in order.** Grouping the pass's anime by queue and waiting between groups is what
  "the later queue's searches wait for the earlier queues'" means, with the same
  `maxConcurrentAnimes` inside each group. The disk budget is shared and reserved in that order too
  ([#101](101-the-disk-guard-counts-what-the-queue-still-has-to.md)), so an earlier queue's packs
  get the space first. Downloads keep one limit (`max_concurrent_downloads`) and the client's own
  queue: the user's rule.
- **A ranking per queue.** The ranking was package state in `nyaa` (`SetPriorities`), read deep in
  the comparators. The comparators now take the ranking; the `...By` functions sort with a queue's,
  the old names with the active one. The scrape still sorts with the active ranking (it has no
  anime), and the daemon sorts again with the queue's when the anime is in one: a full stable sort
  by the queue's criteria, so the scrape's order doesn't leak into it. The default queue sorts
  exactly as before (`rankSeriesResults` only re-sorts a FINISHED series,
  [#107](107-source-ranks-finished-series-empty-source-is-web.md)), so a user with no queue sees no
  change.
- **The ignore list is shared.** It filters rows inside the scrape, before any anime is known, so it
  can't be per queue without a second scrape. A queue's copy of it is ignored, and the Priorities
  page hides it for a queue other than the default.
- **Their own store.** `PUT /config` replaces the whole config with what the Settings page loaded
  ([#161](161-priorities-and-notifications-save-only-their-part.md)). Queues in `config.json` would
  be wiped by any Settings save from a tab opened before the queue was made. The store also holds
  membership (each queue lists its anime), so the picker preview, the manual download and the pass
  find an anime's queue with no FileManager, and deleting a queue drops its list: its anime are in
  the default queue again with nothing else to rewrite.
- **The default queue holds everything else.** Anime from AniList and MyAnimeList lists never go
  through Add anime, so they land in the default queue, and moving one from its page lists it in
  another queue. The default queue's ranking is `config.json`'s `priorities`, so the Priorities
  page, `priorities set` and `PUT /config/priorities` keep working unchanged. It can't be deleted;
  it can be renamed and moved in the order.
- **Add anime ranks with the queue it chose.** The anime is in no queue when Add searches, so the
  first search (`GET …/torrent-candidates?queue=`, `PreviewWholeTitleTorrentsInQueue`) names the
  chosen queue, and so does the picker's own search. The add then puts the anime in the queue
  before its download, and a refused add puts it back where it was: like the id
  ([#108](108-empty-first-search-skips-the-picker-custom-q-drops.md)), a refused add leaves no
  trace. With a queue choice on screen the add always sends it, Default included, so an anime an
  earlier add left in another queue goes where the screen says.
- **The pack picker ranks as the pass.** The whole-title list is the pass's own choice offered to
  confirm ([#160](160-a-manual-episode-download-ignores-the-size-and.md)), so a FINISHED series
  ranks there with its source too, whatever its queue (#107). Before queues the picker skipped that
  second sort for every anime.
- **Ids, not names.** A queue keeps the id made from its first name (`QueueID`), so renaming
  rewrites nothing else.

**Don't "fix" by:**
- Moving the queues into `config.json`: a Settings save from an older tab deletes them.
- Ordering the client's download queue by the anime's queue: the user decided downloads share one
  limit and start as slots free, whatever their queue.
- Running every queue's anime in one parallel pool "for speed": the order would no longer hold back
  the later queues' searches.
- Making the ignore list per queue inside the scrape: the scrape doesn't know the anime, and a row
  dropped there is gone for every queue.
- Putting the anime's queue in `anime_settings`: the preview and manual download paths don't load
  it, and deleting a queue would have to rewrite every anime's settings.
- Giving the default queue a ranking of its own in `queues.json`: it would shadow `config.json`'s
  `priorities`, which four other paths write (the Priorities page, `priorities set`, `PUT
  /config/priorities` and `PUT /queues/default/priorities`).

**Amendment (2026-11-27, the Priorities page lists the queues first):** a queue's criteria open on
a click on it, and **Same criteria for several queues** (the page) and `queues priorities copy
<from> <to>... | --all` (the CLI) give one set of lists to several queues. The copy is a **one-time
stamp**: the queues are not linked afterwards, each keeps its own ranking and can be changed on its
own, which is what the user asked for ("making the criteria for some or all lists the same, which
can be modified further individually"). The ignore list never travels (it is the default queue's,
shared). The CLI sends one partial `PUT /queues/{id}/priorities` per target, without the ignore
list and without null lists (a null would clear the target's list): not atomic, so a failure part
way leaves the earlier targets changed and says which queue failed; running it again finishes it.
Don't "fix" by linking queues to a shared ranking (the per-queue edit after the copy is the point)
or by adding a batch endpoint for a convenience the per-queue PUT already serves.
