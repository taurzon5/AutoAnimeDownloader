# A multi-season pack wins a single-season entry

Found on 2026-11-22 while recording the golden case `kimetsu-season-range-pack`. Not started.
Delete this file when it is fixed or dropped.

## Symptom

For `Demon Slayer: Kimetsu no Yaiba` (AniList **101922**, season 1, 26 episodes) the pass picks
`[Trix] Kimetsu no Yaiba S01-05 [Dual Audio] [Multi Subs] (BD 1080p AV1) - Demon Slayer VOSTFR
(Batch)` for episodes 1-12: 19.9 GiB, 63 files, five seasons. On the same results page there are
season-1-only packs: `[sam] Kimetsu no Yaiba Season 1 | Demon Slayer (BD 1080p HEVC x265 10-bit
FLAC) [Dual-Audio]` and `[Erai-raws] Kimetsu no Yaiba - 01 ~ 26 [1080p][HEVC][BATCH]…`. Watching
season 1 costs the disk of the whole franchise.

## Cause

Not a parsing bug. Since `aec2064` the range comes from the file list (`PackFileRange` flattens the
restart-per-season groups to 1..63). That range covers the 1-12 window, a pack that covers more than
the window is accepted on purpose (it holds the next cours —
[decisions/078](../agents/decisions/078-a-torrents-unit-of-ownership-is-coverage-on-the.md)), and
then the ranking decides on seeders and quality, where Trix (435 seeders) wins. Nothing in the
choice weighs how much of the pack is outside the anime.

## Decided (2026-11-26)

The user's answer: identify a franchise pack and map its episodes to every entry it carries, so one
download fills all those seasons instead of being weighed against one season's pack. That is the
[megapack spec](../superpowers/specs/2026-11-23-megapack-downloads-design.md) (recognition, franchise
groups, mapping files to entries, file selection); this note goes when it lands.

## Progress (2026-11-27)

Megapack step 1 is built ([decisions/163](../agents/decisions/163-a-megapack-is-a-torrent-whose-files-map-onto-two.md)):
the picker shows the Trix pack's mapping table (which season each file belongs to). The pass still
downloads the whole pack for a season-1 entry; that ends with file selection (spec step 2,
qBittorrent's file priorities).

## Progress (2026-11-27, later)

Megapack step 2 is built ([decisions/165](../agents/decisions/165-a-torrents-files-are-chosen-with-qbittorrents-file.md)):
with qBittorrent, picking the Trix pack by hand can untick the four other seasons, and the files of
a pack already downloading can be unticked from the Downloads page. The pass still takes the whole
pack for a season-1 entry: choosing files there is spec step 3 (with "Download related seasons").

## What to check

- Whether "covers the window" should prefer, all else close, the pack whose span is nearest the
  entry's total (26 here) over one that carries 63. That might be a tie-break or a penalty in
  `chooseCoveringPacks`, not a hard filter: a franchise pack can be the only live one.
- Whether the size ceiling (`max_batch_torrent_size_gb`, default 100) should be per covered episode
  instead of per torrent. That overlaps
  [download-part-of-a-pack.md](download-part-of-a-pack.md).
- Pin it: the golden case `kimetsu-season-range-pack` then gets `picked` set to the season-1 pack.
