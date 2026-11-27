# 148. Shoko's change events refresh only the series they name, after they settle, and the daily refresh stays for what the events don't tell

**Location:**
- `internal/shoko/watch.go`: `Client.Watch` (the SignalR client), `parseHubMessage`, and for Shoko
  6 `shoko6Events` and `parseShoko6Change`; `client.go`: `Client.SeriesOfAniDB`.
- `internal/shoko/store.go`: `Store.RefreshSeries`, and `syncMu`, which runs it and `Sync` one at a
  time; `Store.SeriesForChange` (Shoko 6).
- `internal/daemon/shoko.go`: `ShokoService.Watch`, `settleAndRefresh`, `catchUp` and `rematch`,
  started by `cmd/daemon/main.go`; for Shoko 6, `seriesOfChange`, `resolveAniDB` and the queue's
  `pendingAniDB`.
- Config: `shoko_watch`.

Pinned by:
- `TestWatchReportsTheSeriesOfFileEvents`, `TestParseHubMessage` and `TestStoreRefreshSeries`.
- `TestShokoWatchRefreshesOnAChange`, which includes a burst of three events read as one refresh.
- `TestShokoWatchStaysOffWhenTurnedOff` and `TestShokoWatchClosesWhenTurnedOff`.
- `TestShokoWatchCatchUpSeesADeletionMadeWhileAway`, `TestWatchQueueKeepsEveryEvent` and
  `TestShokoOffClearsWhatRematchUses`.
- `TestShokoLiveWatch`, against a real Shoko.
- `TestShokoFullResyncEveryNDays` and `TestStoreFullDue`.

**What it looks like:**
- Shoko says a file changed, but the app neither runs a sync nor starts a check pass. It reads
  only that series' episodes, and only some seconds later.
- With live events on, the daily refresh (`shoko_refresh_hours`) still runs.
- A reconnection reads every followed series again, where the daily refresh reads only new files.

**Why it's right:**

- **What Shoko sends, measured on v5.3.3.** The hub is `/signalr/aggregate`, and the file events
  are on the `shoko` feed; the `file` feed sends none of them. A file linked to an episode sends
  `ShokoEvent:FileMatched`, and a deleted file sends `FileDeleted`; both name the series in their
  cross-references. `FileDetected` and `FileHashed` come earlier, and don't name a series yet.
  Unlinking a file from an episode sends **nothing**. The connected signal is the handshake answer
  (`{}`): `OnConnected` comes only with some feeds.
- **Only the series named, because a deletion matters.** The newer-only sync reads files imported
  since the newest seen, so a file deleted in Shoko is invisible to it until a full resync. The
  event names the series, and reading that series' episodes again (`RefreshSeries`, one request)
  sees the deletion and the addition alike. It is cheaper than a sync too.
- **After the events settle (10 s).** A season imported at once is one event per file. Waiting
  until no event came for 10 s makes it one refresh per series, not one per file.
- **No pass.** A file added in Shoko only means the app won't search for that episode, and the
  next pass already reads the store. A file deleted means the episode is to download. The next
  pass does that within `check_interval`, as for any new episode, rather than an extra pass of
  AniList and Nyaa requests per change in Shoko. The anime page is updated at once: `rematch`
  works the matches out again from the last pass's anime.
- **What catches what.**
  - *Live events:* a file linked or deleted while connected.
  - *The catch-up after every (re)connection* (`catchUp`): the newer-only sync, then the episodes
    of every series a followed anime matches, read again. The second part is what sees a deletion
    or an unlink made while away, which the newer-only sync can't. It costs one request per
    matched series, and reconnections are rare.
  - *The daily refresh* (`shoko_refresh_hours`): newer-only, as the user asked ("after one full
    sync, only scan for newer files and series"). It catches files imported while nothing listened
    (`shoko_watch` off, or Shoko unreachable), never a deletion or an unlink.
  - *An unlink while connected sends nothing:* it is seen at the next reconnection, or by a full
    resync, now also automatic every `shoko_full_resync_days` (30 by default, `Store.FullDue`, the
    spec's proposed default). The gap is at most that long.
  - The watcher backs off from 5 s to 5 min while Shoko is unreachable.
- **No event is dropped.** The events go into a set the refresher takes whole (`watchQueue`), not
  a bounded channel: a burst bigger than any buffer would otherwise lose a `FileDeleted`, and only
  a Full resync would bring it back.
- **The config is read while connected too** (`configCheck`, 30 s). Turning `shoko_watch` or Shoko
  off, or signing in to another server, closes the open connection, instead of leaving it until it
  drops on its own. Shoko turned off also clears the last pass's inputs, so a late event's `rematch`
  can't bring the anime page's matches back.
- **One missing series doesn't sink a batch.** `RefreshSeries` tries every series. One Shoko
  answers 404 for (measured: a missing series id gives 404 on both `Series/{id}` and its
  episodes) is dropped from the store, and another error is returned only after the rest were
  read. An event that comes before the first pass, while nothing yet says which series are
  followed, stays queued and is retried every settle time, rather than dropped. It is put back
  quietly (`watchQueue.keep`, no kick): a kick with the reconnection's catch-up flag fires at once,
  and the refresher would spin until the first pass. Pinned by
  `TestShokoWatchWaitsForTheFirstPassWithoutSpinning`,
  `TestStoreRefreshSeriesGoesPastAMissingSeries` and `TestShokoWatchKeepsEventsUntilTheFirstPass`.
- **One sync at a time.** `Sync` and `RefreshSeries` each read the state, ask Shoko, then write the
  state back. Run together (a pass's sync and a refresh after an event), one would overwrite the
  other, so `syncMu` makes them take turns.

**Amendment (2026-11-27, Shoko 6.0.0):** Shoko 6 has no `shoko` feed and renamed the events
(measured on v6.0.0, and the same names Shoko's own web UI listens to): a link **and** an unlink
send `release:saved` / `release:removed` on the `release` feed, a deletion `file:deleted` on the
`file` feed. They don't name a Shoko series: the payload is the file id and the release's
cross-references as AniDB URIs (`anidb://series/20035`). The client asks for
`shoko,file,release` (a hub ignores a feed it doesn't know, so one list serves 5 and 6), reads both
shapes (`parseShoko6Change`), and the daemon turns AniDB and file ids into Shoko series
(`Store.SeriesForChange`). An AniDB anime the store never read waits in the queue
(`pendingAniDB`) and is looked up once the events settle (`resolveAniDB`, `Client.SeriesOfAniDB`, a
lookup in Shoko's own database, no AniDB request): a season of a new series costs one lookup, and a
failed lookup stays queued for the next refresh, so no event is dropped there either; one Shoko has
no series for names nothing to refresh. On 6 an unlink is seen at once. The 5.3.3 facts above
still hold for 5.x. Pinned by `TestWatchReadsShoko6Events`, `TestStoreSeriesForChange`,
`TestSeriesOfAniDB`, `TestShokoWatchFollowsShoko6Events` and `TestShokoLiveWatch` (run on v6.0.0).

**Don't "fix" by:**
- Asking for the `shoko` feed alone again: Shoko 6 ignores it, and the app would hear nothing.
- Dropping a change that names no Shoko series: on Shoko 6 none does.
- Subscribing to the `file` feed **instead of** `shoko`: on 5.3.3 it carries no file events (it is
  in the list for Shoko 6's `file:deleted`).
- Waiting for `OnConnected` as the connected signal.
- Running a full or newer-only sync on every event.
- Refreshing on `FileDetected`/`FileHashed`.
- Starting a check pass per event.
- Dropping the daily refresh because events exist.
- Turning the catch-up into the newer-only sync alone.
- Going back to a bounded channel that drops events.
- Reading the config only between connections.
- Letting `RefreshSeries` run beside `Sync`.
