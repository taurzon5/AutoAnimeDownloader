# An AniList custom list as a download source

Found on 2026-11-25 while asking how to add an AniList custom list to what the app downloads. Not
started. Delete this file when it is fixed or dropped.

## What the user asked

Add an AniList custom list to what the app downloads: every anime in that custom list downloads,
whatever its status on AniList. Not tied to a release yet.

## What exists today

- An account's anime are fetched by **status**: `GetAllCurrentAnime`
  (`src/internal/anilist/anilist.go`) asks AniList for `mediaList(… status_in: …)` with
  `download_statuses`, Watching and Rewatching by default. An anime in a custom list but marked
  Planning, Paused or Completed is never fetched.
- Custom lists only work the other way: `excluded_lists` ("Excluded List" on the Configuration
  page) skips an anime that is in a named custom list (`animeIsInExcludedList` in
  `src/internal/daemon/helpers.go`, called from `shouldSkipEpisode`; `mergeAniListAnimes` for
  `GET /api/v1/animes`).
- Each entry's `customLists` flags already come back with the list query, and
  `GetCustomListsMap` fetches them with a lighter query
  ([decisions/011](../agents/decisions/011-getcustomlistsmap-separate-lightweight-query-cache.md)).
- The [release-filters spec](../superpowers/specs/2026-11-23-release-filters-design.md) §3 lets
  its lists take an AniList custom list as a source, but "a source only brings anime the app
  downloads at all", so it doesn't change what downloads.

## Decided (2026-11-26)

- **Status doesn't matter.** An anime in the included custom list downloads whatever its AniList
  status, Completed and Dropped included. An excluded list still wins over it.

## What to check

- **The setting.** Custom list names belong to one account, so pair the name with its account
  (`{"account": "anilist:me", "custom_list": "To download"}`, as the spec's list sources do). A
  bare name would match a list of the same name on every account.
- **The query.** Fetch the account's entries whatever their status (drop `status_in`, or a second
  query for the statuses `download_statuses` leaves out) and keep the ones flagged in the included
  list. That is one more AniList query per account per check: weigh it against the AniList budget
  ([decisions/065](../agents/decisions/065-anilist-budget-is-spent-by-the-orphan-refresh-not.md),
  [decisions/072](../agents/decisions/072-anilist-budget-is-measured-from-the-response.md)) and
  the list caches.
- **Status and list together.** Planning is probably the main case (a "to download" list of shows
  not started yet). Decide what Completed or Dropped means for an anime in an included list. An
  anime in an included and an excluded list stays out, as "except beats match" in the spec.
- **Progress.** A Planning entry has progress 0, so the episode limit and deleting watched
  episodes start from episode 1.
- **The Status page.** `GET /api/v1/animes` filters not-yet-downloaded anime by
  `download_statuses` on AniList's side (`refreshOrphanAnimes`). These anime have to show there
  too, and under the user filter of their account.
- **MyAnimeList** has no custom lists, so this is for AniList accounts only, as excluded lists are.
- **CLI.** A new `config.json` field is settable through `config set` already.
- **Queues.** A user-made queue ([user-made-download-queues.md](user-made-download-queues.md))
  could take a custom list as its source.
