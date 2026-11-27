# A season range in a pack name reads as one season

Found on 2026-11-22 while fixing the `S01-05` episode range (`aec2064`). Not started. Delete this
file when it is fixed or dropped.

## Symptom

`ExtractSeason` gives a multi-season pack a single season, or none:

| Name | `ExtractSeason` | Carries |
|---|---|---|
| `[Trix] Kimetsu no Yaiba S01-05 … (Batch)` | 1 | seasons 1-5 |
| `[Group] Anime Season 1-3 [BD 1080p] (Batch)` | 1 | seasons 1-3 |
| `[Group] Anime Seasons 1 ~ 3 [BD 1080p]` | none | seasons 1-3 |

The first two matter. On the pack path, a requested season drops a pack whose season differs
(`ScrapNyaaForAnime`: `requestedSeason != nil && (season == nil || *season != *requestedSeason)`).
So for a season-3 entry the Trix pack, which contains season 3, is dropped as "season 1". Not
measured live. It is inferred from the filter, and the golden case only covers season 1.

## Cause

`reSeasonPatterns` (`nyaa/nyaa_regex.go`) takes the first number after `S` / `Season`. `Season\s*`
also misses the plural `Seasons`. `BatchInfo` has one `Season int`, so the range has nowhere to go.

## Progress (2026-11-26)

- **Read:** `nyaa.ExtractSeasonRange` reads `S01-05`, `S01-S05`, `Season 1-3` and `Seasons 1 ~ 3`
  (`TestExtractSeasonRange`). `ExtractSeason` is unchanged: teaching it the plural would make
  `Seasons N` a title cut point and `Seasons 2 & 3` a season-2 pack, letting franchise packs in early
  ([decisions/083](../agents/decisions/083-the-pack-names-range-is-the-first-plausible-match.md)).
- **Not used yet:** the pack-path filter still drops a franchise pack for a later season. The user
  decided (2026-11-26) that a franchise pack should have its episodes mapped to every entry it
  carries, which is the [megapack spec](../superpowers/specs/2026-11-23-megapack-downloads-design.md):
  the filter changes with its step 1, not before, so a later season can't start downloading a whole
  franchise for one season's episodes.
- **Live:** the golden case `kimetsu-season2-range-pack` (AniList 129874) showed the app's season-2
  queries never surface `[Trix] … S01-05`; a franchise pack named `(Season 1+2+3+4+5+Movie)` does
  show up, and the spec's `S01+S02+…` shape covers it.

## Progress (2026-11-27)

Megapack step 1 ([decisions/163](../agents/decisions/163-a-megapack-is-a-torrent-whose-files-map-onto-two.md))
reads plus lists too (`S01+S02+S03`, `Season 1+2+3+4+5+Movie`) and shows a franchise pack's files
per entry in the picker. The pack-path filter still waits: the pass doesn't act on the mapping, and
taking the pack for a later season would download the whole franchise until file selection (spec
step 2) exists.

## What to check

- Whether `ExtractSeason` (or a new `ExtractSeasonRange`) should return lo..hi, with the pack-path
  filter accepting a requested season inside the range. A pack with seasons restarting is then
  placed on the series axis by `packAxis` / `PackFileRange` ([decisions/116](../agents/decisions/116-restart-per-season-packs-flatten-onto-the.md)).
- The guard `reSeasonRangePrefix` already recognizes the same three shapes on the episode side, so
  the two should share a pattern.
- Pin it with a golden case on a season-2+ entry of Kimetsu (`make golden-record`), with `picked`
  set before the fix. The unit test `TestExtractBatchInfo_ExtractsRangeAndSeason` currently asserts
  season 0 for `Seasons 1 ~ 3`, which is today's behavior; change it with the fix.
