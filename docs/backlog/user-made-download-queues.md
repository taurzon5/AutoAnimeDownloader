# Queues made by the user, each with its own order and priorities

Found on 2026-11-25 while looking for the app's queues in the test instance. Not started. Delete
this file when it is fixed or dropped.

## What the user asked

- The app starts with one **default queue**. The user makes more queues later.
- The queues have an **order**: the user sets which queue downloads first.
- Each queue has its own **priority options**, set once for every anime in it (the ranking the
  Priorities page sets: resolutions, fansubs, codecs and so on).
- **Adding an anime** asks which queue it goes to.

Not tied to a release yet.

## What exists today

- One download queue for everything: `max_concurrent_downloads` (3). The app writes it into
  qBittorrent as `max_active_downloads` when it manages qBittorrent's settings, and the built-in
  client runs its own queue with it. The Downloads page shows "Queued #N", and
  `POST /api/v1/torrents/prioritize` (`torrents prioritize`) moves torrents to the front.
- One ranking for every anime: `priorities` in `config.json` (the Priorities page).
  `nyaa.SetPriorities` stores it in the nyaa package, and `SortTorrentResults`,
  `SortFinishedSeriesResults` and `SortMovieResults` read it from there, not from an argument. Getting one anime a different
  release means reordering the ranking for all of them, then removing and adding the anime again
  ([pick-a-reference-release-from-all-results.md](pick-a-reference-release-from-all-results.md)).
- Add anime (`POST /api/v1/standalone-animes`, `animes add`) takes the media id and, optionally,
  a magnet. Per-anime settings (`AnimeSettings`) already hold the download folder picked on the
  add screen (`root`).

## Overlap with the release-filters spec

[Release filters](../superpowers/specs/2026-11-23-release-filters-design.md) §3 "Lists" designs
lists made in the app: anime picked one by one, whole AniList or MAL lists, and "added by hand".
§4 gives filters with priorities that apply to any selection of lists. Queues are close to those
lists. What differs:

- a default queue exists from the start, and every anime is in exactly one queue (the spec lets an
  anime be in several lists, and one in no list downloads as today);
- the order of the queues decides what downloads first; the spec's lists have no order;
- a queue carries a ranking itself; in the spec, filters accept or refuse, and point at lists;
- the queue is picked when adding the anime, not on a Lists page.

Decide whether queues are the spec's lists plus an order and a default, or a separate thing,
before either is built.

## Decided (2026-11-26)

- **One shared download limit.** All queues share `max_concurrent_downloads`; there is no limit per
  queue.
- **The order holds back searching, not downloading.** The searches of a later queue wait for the
  earlier queues' searches; once found, downloads start as slots free up, whatever their queue.

## What to check

- **What "first" means.** Torrents of an earlier queue start before those of a later one: the app
  sets qBittorrent's queue positions (it already moves torrents to the front for Prioritize) and
  orders the built-in client's queue the same way. Decide whether a later queue also waits for the
  earlier queues' searches, or only for their downloads, and whether each queue gets its own
  number of downloads at a time or they all share `max_concurrent_downloads`.
- **Anime from an AniList or MAL list** don't go through Add anime. Either they land in the default
  queue, or a queue takes whole account lists, as the spec's lists do.
- **Moving and deleting.** Move an anime to another queue from its page. Deleting a queue moves its
  anime to the default queue, and the default queue can't be deleted.
- **A ranking per queue.** The default queue keeps today's `priorities`, and a new queue starts as
  a copy. The sort functions take the anime's queue's ranking as an argument instead of the nyaa
  package's single copy.
- **The name.** "List" collides with AniList's lists and the spec's lists, and "queue" with
  "Queued #N" on the Downloads page. Pick one.
- **CLI parity.** Creating, renaming, reordering and deleting a queue, setting its ranking, and the
  queue on `animes add` each ship with their CLI command in the same change
  ([conventions.md](../agents/conventions.md)).
