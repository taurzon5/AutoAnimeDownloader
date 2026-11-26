# 155. The app's blacklist stops checking and downloading an anime and keeps its files

**Location:** `internal/daemon/blacklist.go` — `BlacklistAnime` (pauses the anime's unfinished
torrents, `unfinishedTorrentsOnlyOf`), `LiftBlacklist` (resumes only those), the pass filters
`withoutBlacklistedAnimes`/`withoutBlacklistedIDs`/`withoutAnimes`, `keepBlacklistedPaused`;
`internal/daemon/verification.go` — `AnimeVerification` filters the standalone ids, the on-disk ids and
the anime list, calls `keepBlacklistedPaused`, and passes the set to `handleSavedEpisodes`
(`episodes.go`); `internal/files/filemanager.go` —
`AnimeSettings.Blacklisted`, `Blacklisting`, `BlacklistedIDs`; `internal/api/endpoint_blacklist.go` —
`handleAnimeDelete`, `handleBlacklist`, `handleBlacklistRemove`; `endpoint_standalone_animes.go` —
the `BLACKLISTED` 409 and `unblacklist`. Pinned by `TestBlacklistPausesOnlyTheAnimesUnfinishedTorrents`,
`TestKeepBlacklistedPausedCatchesRestartedTorrents`, `TestHandleSavedEpisodes_AppBlacklistKeepsFiles`,
`TestAnimeVerification_SkipsBlacklistedAnime`, `TestAnimeVerification_PausesBlacklistedTorrentRunningAgain`,
`TestAnimeDelete_*`, `TestBlacklistEndpoints`, `TestStandaloneAdd_BlacklistedAsksFirst`,
`TestStandaloneAdd_RefusedAddKeepsBlacklist` and the
frontend's `AnimeDetail.delete.test.ts`, `Blacklist.test.ts` and the blacklisted cases of
`AddAnime.test.ts`.

**What it looks like:** a second blacklist next to `excluded_lists`, which already makes the Status
page say "Blacklisted"; one that deletes nothing although that one deletes; a blacklist stored in the
per-anime settings instead of a file of its own; and a "Delete" that on a list anime doesn't delete
the anime.

**Why it's right:**

- **The user's rule: "blacklisting would just stop downloading and checking".** An anime added by
  hand has no AniList list to put in `excluded_lists`, and an install without an AniList account has
  no custom lists at all. The app's blacklist works for every anime, and does only that: the pass
  doesn't fetch it as a standalone, ask its list status, search Nyaa for it, download it or vote on
  its status, and no automatic delete touches its files. Its episode records still seed the series
  index (`resolveSeriesIndex`), on purpose: another anime of the series can adopt what its packs
  cover ([#78](078-a-torrents-unit-of-ownership-is-coverage-on-the.md)). Its files stay (`handleSavedEpisodes` would otherwise see them as "not in
  watching", since the anime left the pass). Deleting files is a separate tick in the same dialog.
  `excluded_lists` keeps its own behavior ([#121](121-the-blacklist-deletes-what-the-daemon-downloaded.md)).
- **Unfinished torrents are paused, and only those come back** (the user's choice, 2026-11-25).
  `Blacklisting.Paused` holds what the blacklist paused, so lifting it resumes those and not a
  torrent the user had paused already. A finished torrent keeps seeding. A pack that also holds
  another anime's episodes is not paused: that would stop the other anime too. A stopped torrent
  can't be told apart from one the user paused, so the click leaves it; but some stopped by the app
  start again on their own (waiting for the incomplete folder or a download root, a qBittorrent
  torrent waiting for its renames), and a pass already running can add a torrent right after the
  click. `keepBlacklistedPaused` pauses those at the start of the next pass and records them. Until
  then they run: that window, one check interval at most, is the accepted ceiling.
- **"Delete" on a list anime can't remove it**: the next check brings it back from the list. So the
  dialog says which account brings it and offers what the app can do: delete its files (blocked, as
  deleting an episode always is, so they don't download again) and blacklist it, ticked by default.
  With neither, the API answers 409 `ON_A_LIST` instead of a success that did nothing. An anime added
  by hand is removed, its files ticked by default (the user's choice).
- **Adding a blacklisted anime asks** (the user's choice). The search marks it, the screen asks, and
  the add carries `unblacklist` (the picker gets it through `pickerCache`). The blacklist is lifted
  only once the anime is tracked, so an add refused on the way (bad magnet, disk guard, the client
  refusing) leaves it blacklisted, as [#108](108-empty-first-search-skips-the-picker-custom-q-drops.md)
  leaves the id unwritten. This differs from
  `excluded_lists`, which the add simply overrides
  ([#49](049-standalone-anime-tracked-by-the-list-is-the.md) (c)): that one is a list on AniList the
  user may not remember, this one is a choice made in this app, and undoing it is worth a question.
- **Stored in `anime_settings`.** It is per-anime state like the root override, the settings PUT
  merges over it, and a new data file would mean a new path in `files.NewManager` and every test
  that builds one. Name, cover and format are kept with it because an anime deleted and blacklisted
  has no other record left for the Blacklist page.
- **One chip for both.** `is_blacklisted` is set for either blacklist so the Status page's chip and
  filter, and `animes --type blacklisted`, cover both; `app_blacklisted` tells the anime page which
  one it can lift.

**Don't "fix" by:**

- Folding it into `excluded_lists` (or making `excluded_lists` stop deleting): the two are different
  intents, and #121 explains why that one deletes.
- Keeping a blacklisted anime in the pass and skipping it per episode, as `shouldSkipEpisode` does
  for `excluded_lists`: it would still cost the AniList lookups and the status vote, and every
  delete rule would still see it.
- Dropping the `withoutAnimes` guard in `handleSavedEpisodes` "because the anime isn't in the pass":
  that is exactly why its episodes would be deleted as off-list.
- Resuming every paused torrent of the anime when the blacklist is lifted: it would undo the user's
  own pauses.
- Pausing stopped torrents at the click "to be sure": that records the user's own pauses too, and
  lifting would resume them. The per-pass pause catches the ones that start again.
- Lifting the blacklist before the add is accepted.
- Letting "Delete" on a list anime report success with nothing ticked, or quietly blacklisting it.
- Blocking the add of a blacklisted anime outright, or lifting the blacklist without asking.
