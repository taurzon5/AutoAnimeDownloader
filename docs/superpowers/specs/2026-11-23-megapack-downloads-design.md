# Megapack downloads: one torrent for a whole series, and only the parts you want

**Date:** 2026-11-23
**Status:** step 1 (mapping, no new download behavior) built on 2026-11-27
([decisions/163](../../agents/decisions/163-a-megapack-is-a-torrent-whose-files-map-onto-two.md));
steps 2-4 not started — the user's decisions are recorded at the end; step 2 depends on the
qBittorrent backend, which becomes the only client (rain is being dropped)
**Origin:** "create a spec for megapack downloads, and how to achieve that". It is also the design
the qBittorrent backend spec (`docs/superpowers/specs/2026-11-23-qbittorrent-backend-design.md` on
branch `feat/qbittorrent-backend`) and the
[download-part-of-a-pack](../../backlog/download-part-of-a-pack.md) backlog entry wait for:
"per-file selection waits for the user's design".
**Related:** [release-filters](2026-11-23-release-filters-design.md) (filters, lists, the daily
sweep, the search queue, upgrades), [direct-library-mode](2026-11-22-direct-library-mode-design.md)
(series layout, placement before the add), backlog
[multi-season-pack-wins-a-single-season-entry](../../backlog/multi-season-pack-wins-a-single-season-entry.md)
and [season-range-in-a-pack-name-reads-as-one-season](../../backlog/season-range-in-a-pack-name-reads-as-one-season.md).
**Expected scope:** `internal/torrents` (file selection through qBittorrent), `internal/nyaa`
(megapack recognition, the cached file list), `internal/anilist` (franchise groups),
`internal/daemon` (the queue's search order, the megapack queue, mapping, records),
`internal/files` (layout, settings), frontend (franchise groups, the megapack picker).

---

## What a megapack is

A **megapack** is one torrent holding more than one AniList entry: several seasons or cours, often
with the movies, OVAs and specials of the franchise, or a long-runner's hundreds of episodes.

```
[Anime Time] One Piece (0001-1071+Movies+Specials) [BD+CR] [Dual Audio] [1080p][HEVC 10bit x265]…   587.5 GiB, 1,509 files
[Trix] Kimetsu no Yaiba S01-05 [Dual Audio] [Multi Subs] (BD 1080p AV1) (Batch)                      19.9 GiB,     63 files
[Judas] JoJo no Kimyou na Bouken (Seasons 1-4 + OVAs) [UNCENSORED BD 1080p][HEVC x265 10bit]…       69.6 GiB,    182 files
[Erai-raws] Bleach - 001 ~ 366 [1080p DSNP WEB-DL AVC AAC][MultiSub] [BATCH]                        321.8 GiB,    366 files
```

A season pack (one entry, or one season split by AniList into cours) is not a megapack; the app
already handles those ([#79](../../agents/decisions/079-pack-choice-asks-does-it-cover-the-window-not-is.md),
[#84](../../agents/decisions/084-a-pack-without-a-range-in-its-name-gets-its.md)).

## Measured on 2026-11-23

10 Nyaa searches (`c=1_2`, sorted by seeders): One Piece, Naruto, Bleach, Gintama, Haikyuu, JoJo,
Attack on Titan, Kimetsu no Yaiba, My Hero Academia, Dragon Ball, each with "batch". 469 rows;
**133 look like megapacks** (a multi-season marker, "Complete", "+Movies/OVAs", an absolute range
like `001-366`, or over 60 GiB).

| | megapacks |
|---|---|
| size, median | **47.7 GiB** |
| size, largest | 1,331 GiB |
| over the 100 GiB pack ceiling | **45 (34%)** |
| over 500 GiB | 5 |
| seeders, median | **5** — most are dead |
| seeders, best | 997, 734, 463, 429, 373 |

File lists of the ten best-seeded (one per franchise), from their Nyaa pages:

| release | files | folders with videos | season folders | extras folders (Movies, OVA, NC…) |
|---|---|---|---|---|
| Anime Time — One Piece 0001-1071 + Movies + Specials | 1,509 | 25 | 20 | yes |
| Erai-raws — Bleach 001 ~ 366 | 366 | 1 | 0 (flat) | no |
| Anime Time — Naruto 001-220 + Movies | 248 | 8 | 5 | yes (Movies, NC/NCED) |
| Trix — Kimetsu no Yaiba S01-05 | 63 | 5 | 5 (`Season 1`…`Season 5`) | no |
| Judas — JoJo Seasons 1-4 + OVAs | 182 | 10 | 4 | yes (`JoJo OVAs [1993]`, `[2000]`) |
| Anime Time — My Hero Academia S01-S07 + Movies + OVAs | 194 | 16 | 7 | yes |
| Anime Time — Gintama 001-367 + Movies | 371 | 2 | 0 | one (`New folder`) |
| Anime Time — Dragon Ball Super S01-05 + Movies | 154 | 7 | 5 | yes |
| (no group) — Haikyuu!! Complete Batch | 60 | 3 | 3 (`First Season`…) | no |

**What it means:**

- A third of the megapacks people seed never pass today's 100 GiB ceiling — and the live ones are
  the big ones. For old long-runners they are often **the only live source**: the loose episodes
  are dead (the One Piece case in [sources.md](../../agents/sources.md)).
- Layouts vary: per-season folders (most), one flat folder with an absolute range (Bleach,
  Gintama), seasons named in words (`First Season`). Movies, OVAs and creditless openings sit in
  their own folders. Mapping files to AniList entries has to work from folders **and** from
  numbering.
- Much of a megapack can be unwanted: creditless openings, seasons already on disk. Skipping
  files is what makes a 587 GiB torrent usable.

## What happens today

- **Seasons of one TV chain already work as one owner.** A pack downloaded under one entry is
  adopted by the other entries of the chain on the series' absolute axis
  ([#77](../../agents/decisions/077-the-per-series-absolute-axis-is-a-bfs-two.md),
  [#78](../../agents/decisions/078-a-torrents-unit-of-ownership-is-coverage-on-the.md)), and
  restart-per-season numbering is flattened onto it
  ([#116](../../agents/decisions/116-restart-per-season-packs-flatten-onto-the.md)).
- **Movies, OVAs and specials inside a megapack belong to nobody.** The axis follows `PREQUEL`
  edges between TV and TV-short entries only ([#71](../../agents/decisions/071-anilist-has-no-franchise-id-the-prequel-chain-is.md));
  a movie is its own AniList entry, reached through other relation types.
- **The size ceiling** (`max_batch_torrent_size_gb`, 100) drops a third of them, before any
  coverage is weighed ([#80](../../agents/decisions/080-the-pack-size-ceiling-is-applied-twice-in-the-nyaa.md)).
- **`S01-05` reads as season 1**, so a season-3 entry drops a pack that holds it
  ([backlog](../../backlog/season-range-in-a-pack-name-reads-as-one-season.md)).
- **Everything downloads.** rain v2.5.1 has no file selection: `AddTorrentOptions` is `Stopped`,
  `StopAfterDownload`, `StopAfterMetadata`, `Sequential`; `Files()` and `FileStats()` only read;
  its piece picker skips only pieces that are done or being written (module source, 2026-11-23).

## The client: qBittorrent, and rain is dropped

The user's decision: **rain goes**, and the app drives qBittorrent. Partial downloads come from
qBittorrent's per-file priorities (`torrents/filePrio`, priority 0 = do not download).

Alternatives weighed and not taken: teaching rain file selection (a fork to maintain), switching
to another Go client library (`anacrolix/torrent`), downloading a whole megapack and deleting the
unwanted files afterwards.

**What dropping rain touches beyond this spec** — each needs its own pass when it happens:

- the qBittorrent backend spec (branch `feat/qbittorrent-backend`) stops being "an alternative"
  and becomes the client;
  its five open questions (minimum version, global settings, disable-seeding, switching clients,
  API keys) block this spec;
- hardlink mode (rain's seeding copy in `.torrents` plus links) has no meaning with qBittorrent
  writing the library in place: direct mode becomes the only library mode;
- the incomplete download folder ([#141](../../agents/decisions/141-staged-downloads-are-copied-into-the-library-while.md))
  maps onto qBittorrent's own "keep incomplete torrents in" folder;
- the decisions that exist only to work around rain (stale resume data
  [#140](../../agents/decisions/140-redownloading-missing-files-adds-the-same-torrent.md), the
  embedded session, `disable_seeding`'s two halves) get reversed or rewritten;
- the Docker image needs a qBittorrent beside the app.

## Design

### 1. Recognizing a megapack

From the name: a season range (`S01-05`, `S01+S02+…`, `Seasons 1-4`, `Season 01+02`), "Complete",
"+Movies/OVAs/Specials", an absolute range past one entry's count (`001 ~ 366`). Confirmed by the
file list (Nyaa's `/view/<id>` page, which the app already reads for packs,
[#84](../../agents/decisions/084-a-pack-without-a-range-in-its-name-gets-its.md)). A file list
never changes, so it is **cached on disk by Nyaa id**: a 1,509-file list is opened once, ever.
This also fixes the season-range backlog entry: the name's range becomes `lo..hi` instead of `lo`.

### 2. Franchise groups

The app groups the entries of one franchise: TV seasons, movies, ONAs, OVAs, specials.

- **Candidates by name:** entries whose titles are similar (the matcher searches already use,
  `nyaa_match.go`, over romaji, English and synonyms).
- **Confirmed by the providers' relations:** an entry joins the group only when AniList's
  `relations` link it to the group (`PREQUEL`, `SEQUEL`, `SIDE_STORY`, `SPIN_OFF`, `ALTERNATIVE`,
  `PARENT`, `SUMMARY`), walked two generations per request like the series index
  ([#77](../../agents/decisions/077-the-per-series-absolute-axis-is-a-bfs-two.md)) and cached the
  same way. Name similarity alone would join unrelated works
  ([#71](../../agents/decisions/071-anilist-has-no-franchise-id-the-prequel-chain-is.md) measured
  `Monogatari` matching `Ore Monogatari!!`). MyAnimeList is a list source only today
  ([#95](../../agents/decisions/095-myanimelist-is-a-list-source-not-a-metadata-source.md)): its
  public list data has no relations, so it confirms nothing until a MAL metadata path exists.
- Each entry keeps its format, episode count, year and titles.

**Entries you don't track** are shown in the group with a **Track** switch, **on by default**. A
tracked group entry is filled preferably from a megapack; when none matches, from lower releases
(a season pack, then single episodes), like any anime.

### 3. Mapping files to entries

Before anything downloads, every video file in the megapack gets one of three outcomes:

| outcome | how |
|---|---|
| **an episode of a TV entry** | the season folder (`Season 3`, `S03`, `Third Season`) picks the entry; else the file's number on the series axis (#77, #116), as packs already do |
| **a movie, OVA or special** | the folder or file name matched against the group's non-TV entries: title similarity plus the year when the name has one (`JoJo OVAs [1993]`) |
| **unmapped** | creditless openings/endings (`NC`, `NCOP`, `NCED`), menus, previews, anything no rule places — listed, never guessed |

The result is a **mapping table**: per entry, which files, how many episodes, how many bytes. The
picker shows it.

### 4. What gets downloaded

**Everything the mapping places is selected** by default: every season, movie, OVA and special of
the group. Unmapped files are not. The setting **Download related seasons** (on by default) decides
whether seasons of the group beyond the ones you track come along; off, only the tracked entries'
files are selected. You can tick and untick files and whole entries in the picker before and after
the download.

With qBittorrent the selection is per-file priorities, so:

- the disk and the download hold only the selected files;
- **ticking more later** (another season of a megapack you already have) only changes priorities:
  qBittorrent downloads the new files, nothing else;
- **deleting** a watched season unticks its files: they leave the disk and stop seeding, and the
  torrent stays while any selected file remains.

### 5. The search order

Per-anime searches run in the search queue ([release-filters](2026-11-23-release-filters-design.md)
§2). For each anime the queue processes, the candidates are taken in this order:

1. **a megapack** of the anime's franchise that a filter accepts;
2. otherwise **a season pack**;
3. otherwise **single episodes**.

One search usually returns all three kinds (packs and episodes already share one list, `nyaa.ScrapNyaaForAnime`),
so the order costs no extra request; a megapack query of its own (`<franchise title> batch`) runs
only when the first page has no megapack row, because the seeders sort buries packs under weekly
episodes (the One Piece measurement in [sources.md](../../agents/sources.md)). An anime still
airing mostly gets single episodes, from the daily sweep.

Filters apply to megapacks like any release (`kinds` gains `megapack`); the tier still ranks first.

### 6. The megapack queue — for the big ones

A megapack above a size threshold does not download from the normal queue. It goes to a
**megapack queue** of its own:

- **only anime on your lists** (the lists made in the app, release-filters §3) can put a megapack
  in it; an anime must be on a list first;
- it has its own budget and time windows, like the search queue (for instance one megapack a week,
  at night);
- the page shows each waiting megapack with its mapping table, the selected size and the free
  space it needs.

The disk guard counts the selected bytes
([#93](../../agents/decisions/093-the-disk-guard-has-two-triggers-and-the-size-one.md),
[#101](../../agents/decisions/101-the-disk-guard-counts-what-the-queue-still-has-to.md)), and with
an incomplete folder the staging drive must hold them too.

### 7. Where the files go

With qBittorrent the library is written in place (direct mode, one folder per series,
[#136](../../agents/decisions/136-direct-mode-places-a-torrent-before-the-add-and.md)):

- the megapack's folders go under the series folder as the torrent lays them out, or as `S01`,
  `S02`… with `rename_pack_season_folders`;
- movies and OVAs of the group go to `Specials`, or to the movie library once direct mode's PR 3
  adds one;
- **records:** every mapped episode of every tracked entry gets its record pointing at the one
  hash, the way coverage adoption already writes them (#78); a movie or OVA is episode 1 of its
  entry. Removal keeps the torrent while any entry still claims a selected file.

### 8. Pages

- **Anime page → franchise group:** every entry of the group with its format, what you have, its
  Track switch, and **Download the whole series**: the megapacks found, with seeders, total size,
  selected size, and the mapping table to tick.
- **The picker** shows a megapack's mapping table under its row, unmapped files included.
- **The megapack queue** page (next to the search queue): waiting megapacks, their selected size,
  budget and windows.
- **The downloaded-torrents card** ([release-filters](2026-11-23-release-filters-design.md) §9)
  shows one card for the megapack on every anime it feeds, with that anime's files and the
  selected bytes against the torrent's total.

### 9. Requests and time

- A megapack's Nyaa page is opened once and its file list cached forever. One Piece's page is
  175 KB ([sources.md](../../agents/sources.md)); 30 KB is typical.
- Megapacks are rarely new uploads, so the daily sweep seldom sees them: they come from the search
  queue and the megapack queue.
- A 300 GiB download takes days; the megapack queue's windows hold it to the hours you choose.

## Delivery

After the qBittorrent backend replaces rain:

1. **Mapping, no new download behavior** — recognition (with the season-range fix), the cached file
   list, franchise groups, the mapping table in the picker. **Built 2026-11-27:** `nyaa/megapack.go`,
   `anilist/franchise.go`, `daemon/megapack.go`, the picker's **What goes where** and
   `animes candidate`. The group is not shown on the anime page yet (step 4), and the pass doesn't
   read it. The season-range filter fix waits for step 2: without file selection, a later season
   taking `S01-05` downloads the whole franchise (the user's hold, decisions/083).
2. **File selection** — `TorrentBackend.SetWantedFiles` through qBittorrent's `filePrio`, selected
   bytes in the disk guard and the progress, ticking later.
3. **Search order and the megapack queue** — megapack → season pack → episodes in the search queue,
   the threshold and the megapack queue, Download related seasons.
4. **Franchise pages** — the group on the anime page, Track switches, Download the whole series.

## Decided (2026-11-23)

| # | Question | The user's answer |
|---|---|---|
| 1 | What megapacks are for | **Searched first in the queue processor**: a megapack a filter accepts, else a season pack, else single episodes; airing anime mostly go per episode |
| 2 | Downloading part of a torrent | **Only with qBittorrent** — and then: **rain is dropped**, qBittorrent becomes the client |
| 3 | What is selected by default | **Everything the mapping places** |
| 4 | Movies, OVAs, ONAs inside | **Grouped with the shows by similar names, confirmed by the providers' relations** |
| 5 | Seasons beyond the tracked ones | **Download related seasons**, a setting, **on by default** |
| 6 | Big megapacks | **A megapack queue of their own**; only anime already on the app's lists can enter it |
| 7 | Group entries you don't track | **Shown in the group, with a Track switch on by default**; filled preferably from megapacks, otherwise lower releases |

## Proposed defaults — say if you disagree

- The megapack queue's threshold is today's pack ceiling, **100 GiB**: above it, a megapack waits
  in the megapack queue.
- The megapack queue starts at **one megapack a week**, no time window.
- MyAnimeList confirms group membership only once the app has a MAL metadata path; until then
  AniList's relations decide.
