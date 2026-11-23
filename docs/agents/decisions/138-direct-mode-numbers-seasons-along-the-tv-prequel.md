# 138. Direct mode numbers seasons along the TV PREQUEL chain and pins a series to its first folder

**Location:** `internal/daemon/placement.go` — `chainSeasons`, `titleSeasonPart`, `seasonNumber`,
`episodeFileName` (the `"season"` numbering), `seriesTitle`, `seriesID`, `pinSeriesDir`;
`internal/anilist/series.go` — `Series.Chain`, `ChainEntry`, `RootTitle`, the titles on
`seriesLink`. Pinned by `TestPlacementRequest_RealMushokuChainKeepsPartsInTheirSeason`,
`TestPlacementRequest_SplitCourNumbering`, `TestPlacementRequest_UnmarkedSequelUsesItsChainPosition`,
`TestPlacementRequest_UnknownChainFallsBackToTheAnime`, `TestRegisterPlacement_PinsTheSeriesFolder`
and `TestGetSeriesIndex_ChainCarriesTitlesRootFirst`.

**What it looks like:** the season of a direct-mode episode is not just "the marker in the
title, else the number of prequels". It walks the whole chain, root first, with a rule per
entry; a split cour's file name adds the episode counts of earlier entries; and a new season of a
series ignores the title AniList now gives the root and reuses the folder name an older torrent
of the series already has.

**Why it's right:** AniList has no franchise id
([#71](071-anilist-has-no-franchise-id-the-prequel-chain-is.md)) and no season number. The chain
from `GetSeriesIndex` is the only order there is, and its entries are not seasons.

- **The rule per entry.** A title marker wins ("Season 2", "2nd Season", "II", read by
  `nyaa.ExtractSeason`). An entry without one continues its prequel's season when it is a later
  part ("Part 2", "Cour 2" — a part is not a new season,
  [#10](010-cour-n-treated-as-part-n-not-as-a-distinct-concept.md)), and starts the next season
  otherwise. The root without a marker is season 1. Counting prequels instead put Mushoku Tensei's
  "…Honki Dasu Part 2" (127720, the second half of season 1) in `Season 02/` with "Mushoku
  Tensei II", the bug #10 already fixed for the search.
- **`"season"` numbering adds the earlier entries of the SAME season** (the chain's episode
  counts): II Part 2 episode 1 is `S02E13`, how TVDB/TMDB number it. The other entries are a
  different season and don't count.
- **Pinned folder.** The series folder is named after the root's title, and AniList edits titles.
  The links are restored with a full TTL ([#92](092-the-on-disk-anilist-cache-restores-with-ttl-zero.md)),
  so an edit reaches the daemon at some later refresh — and a season added after it would get a
  second folder. `pinSeriesDir` reuses the folder of any existing placement with the same series
  id (the root's media id). Once every torrent of the series is gone, the current title is used.

**Known limits:**

- Ancestors are read from their **titles only** (English, romaji). The anime being placed also
  reads its synonyms ([#132](132-season-and-part-are-read-from-the-synonyms-that.md)); an
  ancestor whose marker exists only in a synonym gets the position rule instead. Fetching the
  synonyms of every chain node would make the walk's response much heavier for a rare case.
- The chain follows **TV/TV_SHORT** prequels only ([#133](133-an-ona-entry-counts-its-ona-prequel-in-the-season.md)
  keeps `prequelOf` TV-only), so an ONA sequel of an ONA (JoJo Stone Ocean Part 2) is the root of
  its own chain and gets its own series folder.
- A link cached before titles were fetched has none; `walkSeries` refetches it once
  (#92's amendment). Until then the folder falls back to the anime's own title.

**Don't "fix" by:** counting prequels for the season; treating "Part N"/"Cour N" as a new season;
adding the episodes of the whole chain to the episode number (that is the series' absolute axis,
[#77](077-the-per-series-absolute-axis-is-a-bfs-two.md), not the season's); re-deriving the folder
from the current title when the series already has a folder.
