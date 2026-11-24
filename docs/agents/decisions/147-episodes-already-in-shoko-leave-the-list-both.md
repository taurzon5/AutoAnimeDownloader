# 147. Episodes already in Shoko leave the list both selections walk, unless the app has its own record of them, and their MAL ids come from a query of their own

**Location:**
- `internal/daemon/episodes.go`: `withoutOwnedElsewhere`, and the `ownedInShoko` parameter of
  `processAnimeEpisodes`, applied before both `selectEpisodes` calls.
- `internal/daemon/shoko.go`: `ShokoService.ForPass`, called once per pass in `AnimeVerification`.
- `internal/anilist/shoko_facts.go`: `MALFactsOf` and `EpisodesOfMALIDs`.
- `internal/daemon/standalone.go`: `DownloadStandaloneAnime` takes the service and matches the one
  anime (`ShokoService.OwnedFor`, no sync).
- `internal/api/endpoint_animes.go`: `withShokoOwned`, which counts the episodes in Shoko as there
  for the progress bar and the "behind" count
  ([#88](088-the-progress-bar-derives-downloaded-from-episodes.md)).

Pinned by:
- `TestProcessAnimeEpisodes_EpisodesInShokoAreSkippedAndTakeNoSlot`
- `TestWithoutOwnedElsewhere_KeepsTheAppsOwnEpisodes`
- `TestShokoServiceForPass`
- `TestMALFactsOfAndEpisodesOfMALIDs`
- `TestWithShokoOwnedCountsShokoEpisodesAsThere`

**What it looks like:**
- A new parameter on `processAnimeEpisodes`, threaded through every caller and test, where the
  blacklist map (`blockedMap`) already skips episodes.
- An episode that is in Shoko and also downloaded by the app is not skipped.
- An extra AniList query asks for `idMal` beside the pass's own list query, which could have
  asked for it.

**Why it's right:**

- **Not the blacklist.** A blocked episode still goes through `checkEpisode` and counts toward
  `max_episodes_per_anime`. With episodes 1–6 in Shoko and a limit of 12, the app would download
  only 7–12, and the limit would never move past what the user already has. Dropping the owned
  episodes from the list both selections walk makes them take no slot: 7–18 are downloaded
  (measured by the test).
- **The app's own copy keeps its lifecycle.** An episode the app downloaded before Shoko had it
  (or alongside) has a record and files of its own. Dropping it from the list would take it out
  of watched pruning and the limit's deletions, and its files would stay forever. Shoko's files are
  never the app's to delete: the app never has a record of them, so no rule of the app can touch
  them (the Shoko spec §3).
- **The pass's queries stay as they are.** The search golden set replays recorded AniList answers
  keyed by the request body (`daemon/golden_test.go`). Adding `idMal` to the pass's list query, or
  to `GetMediaByID`, would turn every recorded case into "not in the recording" and require
  re-recording them all from the live services. A query of its own, `MALFactsOf` (`id`, `idMal`,
  `episodes`), sent only when Shoko is on and cached for a day, leaves them untouched. So does
  `EpisodesOfMALIDs`, the counts for a series' other MAL ids, which only series with several MAL
  ids need.
- **What it doesn't do yet:**
  - A pack chosen to cover the pending window is one torrent. A 01–24 pack chosen for 7–18 still
    brings the files of 1–6, with no record of them, so in direct mode they land in the library
    beside Shoko's copies.
  - An owned episode inside the window still takes a slot of the window, as the window is
    `windowEnd(firstPending)`.
  - Both wait for per-file selection, which comes with qBittorrent's file priorities (the megapack
    spec).
  - The debug pass ignores Shoko ([#58](058-runanimedebug-mirrors-processanimeepisodes.md)).
  - Manual downloads don't look at Shoko either: the user picked the torrent.
- **The counts read Shoko's episodes as there.** They have no record, so without
  `withShokoOwned` the progress bar's yellow segment and the "behind" chip would count them as
  still to download, forever.
- **A failed sync doesn't fail the pass.** Shoko being down or the key revoked leaves the last
  sync's state in place (`Store.Sync` records the error, shown by `GET /shoko/status`). The pass
  goes on with it, as with a stale AniList cache.

**Don't "fix" by:**
- Merging the owned episodes into `blockedMap`.
- Dropping an episode the app has a record of.
- Adding `idMal` to the pass's list query or `GetMediaByID` without re-recording the golden set.
- Failing the pass when the Shoko sync fails.
