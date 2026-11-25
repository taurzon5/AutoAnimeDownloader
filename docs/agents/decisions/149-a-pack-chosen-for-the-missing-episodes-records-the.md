# 149. A pack chosen for the missing episodes records the episodes in Shoko it brings, on by default, and only those a pack of known range covers

**Location:**
- `internal/files/filemanager.go`: `Config.ShokoFullPacks` (`shoko_full_packs`, "Download Full
  Megapacks"), `true` in `DefaultConfig`, and `Config.KeepsShokoPackEpisodes`, which an anime's
  `AnimeSettings.ShokoPackEpisodes` overrides (`download` or `remove`,
  [#153](153-a-packs-episodes-already-in-shoko-are-removed-for.md)).
- `internal/daemon/episodes.go`: `shokoOwned`, the Shoko input of `processAnimeEpisodes`, and
  `withShokoEpisodesInPacks`, called only before the second selection, once a pack is chosen.
- `internal/daemon/debug.go`: the same step in `RunAnimeDebug`.
- The Shoko group of the settings page (`Config.svelte`) and the anime page's Shoko panel; the CLI
  sets them with `config set` and `animes settings <id> shoko-packs`.

Pinned by:
- `TestProcessAnimeEpisodes_TrackedPackRecordsTheShokoEpisodesItBrings` and
  `TestProcessAnimeEpisodes_TrackedPackAddsOnlyWhatItCovers`.
- `TestRunAnimeDebugMirrorsTheTrackedPackEpisodes`.
- `TestShokoPackAndPathMapDefaults`, and the frontend's `Config.shoko.test.ts`.
- The per-anime override: see #153's tests.

**What it looks like:**
- Episodes the pass just dropped for being in Shoko
  ([#147](147-episodes-already-in-shoko-leave-the-list-both.md)) come back, but only for the second
  selection.
- A pack of unknown range adds none of them, although `covering` counts such a pack as complete.
- The app keeps records of episodes the user already had in Shoko.

**Why it's right:**

- **The files come anyway.** A torrent downloads whole unless its files are skipped, which only
  removing them does, and only qBittorrent can ([#153](153-a-packs-episodes-already-in-shoko-are-removed-for.md)). A pack chosen for the missing episodes brings the
  episodes in Shoko it covers. In direct mode they land in the library beside Shoko's copies
  whatever the setting. On, they get records like the rest of the pack: they show as downloaded on
  the anime page and follow the app's rules, and the user can delete them there. Removed instead,
  they are skipped or deleted ([#153](153-a-packs-episodes-already-in-shoko-are-removed-for.md)). In a download folder inside one of Shoko's import folders
  ([#152](152-a-library-that-is-a-shoko-drop-folder-releases.md)), Shoko imports them too, and
  the app keeps its records: the user's "wait for shoko to add it and track on our side too".
- **On by default, for the whole list, with a per-anime override.** The user asked for "download
  full megapack" as "a global override for the list", then for "a per anime override to download
  or remove the unneeded episodes" (2026-11-24). The app is built for direct mode, where keeping
  them costs nothing while `auto_delete_downloads` is off: `LiftsEpisodeLimit` ignores
  `max_episodes_per_anime`, and nothing is deleted automatically. An anime set to remove them, or
  the switch off, removes them instead
  ([#153](153-a-packs-episodes-already-in-shoko-are-removed-for.md)).
- **Only the second selection.** That one already records the whole pack with the limit lifted
  ([#48](048-max-episodes-per-anime-never-limited-batches-the.md)). The first selection stays
  without them, so on the pass that picks the pack they take no slot, and the window the packs are
  chosen for (from the first missing episode) doesn't move.
- **Only what a chosen pack covers.** `assignBatches` stops at the first episode no chosen pack
  covers. With 1–5 in Shoko and a 06–11 pack, adding 1–5 would stop it at episode 1, and nothing
  would be downloaded at all (the test measures it).
- **Only a pack of known range.** `covering` takes a pack of unknown range as complete, so that it
  still covers the window ([#84](084-a-pack-without-a-range-in-its-name-gets-its.md)). Nothing
  says it holds the episodes before the window, though, and a record of an episode the torrent
  lacks would read as downloaded with its file missing.
- **What follows from a record.** The next pass keeps the episode, since the app now has its own
  copy (`withoutOwnedElsewhere`, [#147](147-episodes-already-in-shoko-leave-the-list-both.md)).
  With `auto_delete_downloads` on, the episode limit applies, and from then on an unwatched tracked
  episode **takes a slot** of `max_episodes_per_anime`, like any record of the app, until it is
  watched and pruned. Limit 12, 1–5 in Shoko and a 01–11 pack: tracked and unwatched, 1–11 fill 11
  slots on the next pass, so episode 13 waits for the user to watch more. Dropping the recorded
  episodes from the count instead would be dropping an episode the app has a record of, which #147
  forbids. When the pack's torrent can leave doesn't change: `hasUnclaimedContent` counts an
  anime's claim by its episode total, not by its records. A watched episode still gets no record,
  as in any pack.
- **The debug run mirrors it** ([#58](058-runanimedebug-mirrors-processanimeepisodes.md)): the
  same helper and `selectEpisodes` over the first selection. Those episodes keep
  `would_search: false` and get the pack as `picked`.

**Don't "fix" by:**
- Putting the Shoko episodes back before the first selection: they would take slots of
  `max_episodes_per_anime` again.
- Adding every Shoko episode, covered or not: `assignBatches` would stop at the first one no pack
  covers.
- Taking a pack of unknown range as holding them.
- Marking the records "won't be deleted" (`ManuallyManaged`). Such a record never enters the delete
  set, so it would hold the pack's torrent (`canRemoveTorrent`), and with it the files of the rest of
  the pack, for as long as it exists.
