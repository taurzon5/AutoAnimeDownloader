# 132. Season and part are read from the synonyms that name the anime, never from one that is the title of its manga or novel

**Location:** `internal/anilist/anilist.go` — `Media.OwnSynonyms`; in `internal/daemon`,
`seasonPartOf` (`helpers.go`) for the season/part reads. `processAnimeEpisodes` and `RunAnimeDebug`
compute `OwnSynonyms()` once, read season and part from it once, and hand them to the anime search, `adoptCoveredEpisodes`, `partitionSearchResults` and `newPackAxis`; the preview
reads them once with `seasonPartOf`; `searchNyaaForSingleEpisode` (the pass, the debug run and
`ManualDownloadEpisode`) takes the whole `Media` and reads season and part with `seasonPartOf`, so no
caller can hand it raw synonyms; pinned by
`TestExtractAnimeSeasonPart_SteelBallRun_SourceTitleSynonymIsNotAPart` and
`TestOwnSynonyms_KeepsSynonymWithoutMatchingSource` in `src/tests/unit/season_part_test.go`.

**What it looks like:** a synonym silently dropped before reading season and part, while the
dashboard search (`api/endpoint_animes.go`) still uses all of them.

**Why it's right:** AniList lists the source's title among the anime's synonyms. Steel Ball Run
(210482) has the synonym `JoJo's Bizarre Adventure: Part 7–Steel Ball Run`, which is exactly the
English title of its `ADAPTATION` manga. JoJo's "Part 7" numbers the manga's story arcs, but
`ExtractPart` read it as part 7 of the anime: the hard part filter of the episode search then
rejected every release (none says "Part 7"), and the wrong part also fed the pack search and the
offset gate ([#9](009-prequel-offset-gate-requires-part-2.md)). A synonym equal to the title of a
manga, novel or one-shot relation names the source, so its numbers count the source. Anime
synonyms keep feeding season and part as before — Kaguya-sama's "Season 2" is still read. For
dashboard search the source name is a fine hit, so that path keeps every synonym.

**Amendment (2026-11-26):** `PreviewEpisodeTorrents` (the single-episode picker) is a fourth caller of `searchNyaaForSingleEpisode`, with the whole `Media` like the others.

**Amendment (2026-11-27):** a synonym's season is also dropped when it equals the titles' part
and the synonym names no part itself (`ExtractAnimeSeasonPart`). BAKI-DOU Part 2 (210032) is titled
`… Part 2` and has the synonym `BAKI-DOU: The Invincible Samurai Season 2` for the same second
cour; read as season 2, the pack path dropped every pack (`Baki-dou - 14 ~ 25` has no season, the
`S01` packs are season 1) and nothing downloaded. A synonym that says both keeps its season:
`BEASTARS Season 3 Part 2` (187267) is season 3, and its EMBER `(Season 3)` and VARYG `S03` packs
need it. Pinned by `TestExtractAnimeSeasonPart_ASynonymSeasonThatIsTheTitlesPart`,
`TestExtractAnimeSeasonPart_ASynonymWithSeasonAndPartKeepsTheSeason` and the golden case
`bakidou-part2-season-synonym`. Don't widen it to "a part in the titles drops every synonym
season": BEASTARS's titles say `Part 2` too. Known ceiling: with the season gone and only an ONA
prequel, the entry reads as a first season ([#96](096-a-first-season-entry-rejects-later-season-packs.md)),
so a pack labeled `S02` for this cour would be dropped as a later season. The packs Nyaa has for it
say `S01E14-E25`, `S01 Part2` or nothing, which is the numbering the cour really has.

**Don't "fix" by:**
- Dropping synonyms from season/part extraction altogether: some sequels carry their season only in
  a synonym (Kaguya-sama 112641, Attack on Titan Final Season).
- Matching a JoJo-shaped pattern ("Part N–Title") in `rePartPatterns`: the same regex reads torrent
  names, and the evidence that the number is the source's is the relation, not the punctuation.
- Filtering source-title synonyms in `ExtractAnimeSeasonPart` itself: it takes titles and synonyms
  only (the cour rule of the amendment needs nothing more, so it lives there), and the anime
  search (`nyaaSearcher.searchAnime`) has no relations to pass — filtering at the `Media` keeps the
  signatures as they are.
