# Release filters, fewer Nyaa requests, upgrades, and the torrents an anime downloaded

**Date:** 2026-11-23
**Status:** step 1 implemented and merged into master (decision 150), except the `.mkv`
track read, which waits for a choice: the repo already reads `.mkv` headers itself
(`files/mkv.go`), so extending that reader would need no library. Steps 2–6 not started. The
user's decisions are recorded at the end; a few proposed defaults wait for a yes
**Origin:** "spec the filters for different audio languages, subtitles, resolution, audio and
video codec, bitrate, web or br and other conditions i'll tell you later", then "add a section in
the watched anime show which shows which torrents were downloaded and their metadata, for
filters, use autobrr filters as reference" — autobrr being the reference for **choosing** the
torrent — then "design the whole app in a way that less overall requests are made to nyaa, scrap a
previous day releases when a new day starts, only open those that match a criteria or the lists we
setup and don't scrap the rest".
**Related:** [subtitle-language-filter](2026-08-24-subtitle-language-filter-design.md) stays a
separate spec (user's choice). Its second search in `c=1_3` and its description reading for
`MultiSub` releases remain there; this spec's subtitle field reads what the name says.
**Expected scope:** `internal/nyaa` (release parsing, the daily listing, row predicate),
`internal/daemon` (the sweep, the search queue, lists, filter stage, tiers, waiting, following,
upgrades, report codes, the add paths), `internal/files` (config, the releases store, held
releases, pins, the replaced list), `internal/anilist` (episode duration), `internal/api` +
`cmd/cli`, frontend (Lists, Filters, Replaced pages, settings, picker groups, the anime page
section).

---

## What exists today, and why it isn't enough

**Choosing.** The Priorities page **ranks**; it never refuses. A 720p or an unlisted group is
still downloaded when it is the only match. The only things that drop a release are the ignore
list (a name substring), `min_seeders`, the two size ceilings and the disk guard. There is no way
to say "Japanese audio only", "never a hardsub", "BD or nothing for movies", "wait for the
dual-audio release", and nothing differs between anime.

**Requests.** Every check (every `check_interval`, 10 minutes by default), each anime with an
aired episode not yet downloaded gets a full anime search, up to `max_search_pages` (5) pages
while fewer than 3 candidates turn up, plus a per-episode search for episodes still uncovered.
Only the per-episode search backs off after a miss (1 h doubling to 24 h,
[#100](../../agents/decisions/100-an-empty-per-episode-search-waits-before-repeating.md)); the anime
search runs on every check by design. An episode that never appears on Nyaa keeps its anime
searched 144 times a day.

**Memory.** Once something is downloaded, nothing records **which** release it was:
`episodes.json` keeps the hash, the size and the episode range, not the release name, the group,
the Nyaa page or any tag. The anime page cannot say what you have.

## Measured on 2026-11-23

**Tags in release names.** 669 names: page 1 of 9 Nyaa searches (`c=1_2`, sorted by seeders) —
the front page and Frieren, One Piece, Dandadan, Solo Leveling, Jujutsu Kaisen, Spy x Family,
Mushoku Tensei, Kaiju No. 8:

| tag in the name | names | share |
|---|---|---|
| resolution | 667 | 99.7% |
| source (WEB-DL, WEBRip, BD, DVD…) | 415 | 62.0% |
| audio codec | 382 | 57.1% |
| video codec | 368 | 55.0% |
| any subtitle marker (MultiSub, Multi-Subs, ESub…) | 321 | 48.0% |
| streaming service (CR, NF, AMZN, DSNP…) | 276 | 41.3% |
| any audio-language marker (Dual-Audio, DUAL, Dub, JPN…) | 224 | 33.5% |
| container (`.mkv`, MP4) | 195 | 29.1% |
| bit depth (10bit, Hi10) | 81 | 12.1% |
| version / repack (`v2`, REPACK) | 26 | 3.9% |
| **bitrate** | **0** | **0%** |
| **neither an audio nor a subtitle marker** | **255** | **38.1%** |

Groups: SubsPlease 201 (30%), ToonsHub 104, **no bracketed group 93 (14%)** — scene-style names
that end in `-VARYG`, Erai-raws 90, Judas 36. SubsPlease tags none of codec, source, audio or
subtitles: `[SubsPlease] Title - 07 (1080p) [HASH].mkv`.

**View pages.** 12 (`/view/<id>`), one per group with three or more releases: a MediaInfo block
on **3/12** (EMBER, Tenrai-Sensei, AnoZu), a bitrate figure on 5/12, audio-language words on 8/12,
a subtitle list on 5/12. SubsPlease's page has none of them. Nyaa does not search descriptions,
and the file list cannot show tracks muxed inside an `.mkv` (the subtitle spec, August).

**Upload volume.** Nyaa's `c=1_2` listing, newest first, 75 rows a page: page 1 spanned 13.0 h
(~139 uploads a day), page 6 spanned 11.4 h (~158 a day). **A whole day is 2–3 listing pages.**

**What it means for the design:**

- Resolution is always there. Source, codecs, audio and subtitles are there about half the time,
  so the user chooses how "the name doesn't say" is treated (§4).
- **Bitrate is never in the name.** It is estimated: size ÷ (episodes covered × episode length
  from AniList).
- Reading a whole day of uploads costs 2–3 requests. Matching names against every tracked anime
  is local work. That is cheaper than one anime search.
- The **files themselves** are the only reliable source of tracks, and they are on disk after the
  download. The anime page reads them; the filters cannot (the choice comes first).

## The reference: autobrr

autobrr's filters ([docs](https://autobrr.com/filters)) are named sets of conditions checked
against each **announced** release:

- every field set on a filter must match; an empty field matches anything;
- **Except** beats **Match** in every pair (groups, releases, HDR, other, languages);
- lists of values are comma-separated, case-insensitive, with `*` and `?` wildcards; "Match/Except
  releases" also take Go regex;
- checks run from cheap to expensive and stop at the first failure: fields parsed from the name
  first, then data fetched out of band (size, uploader), then external scripts;
- each filter has a **priority**, a **delay**, size limits and its own actions.

autobrr reacts to releases as they are announced; with the daily sweep (§2) this app does the same
with a day's uploads. What differs:

| autobrr | here |
|---|---|
| Filters are independent; any match triggers actions | Candidates for one episode compete: a candidate's **tier** is the priority of the best filter it matches; tiers rank first, `criteria_order` breaks ties within a tier |
| Delay = wait before acting on a match | `accept_after` = a filter only approves once the episode aired, or the release was uploaded, that long ago |
| Size fetched only when the announce lacks it | The view page fetched only when a filter needs a field the name lacks |
| Movies/Shows, Years, Seasons, Episodes | The anime is already known: a filter applies to **lists** (§3) |
| Smart episode, skip duplicates | Already the episode logic; upgrades are §7 |
| Indexers, categories, freeleech, uploaders, max downloads per period | No equivalent (one source, no ratio); dropped |

Kept as they are: the Match/Except pairs, wildcards, regex on the release name, "cheap checks
first", priority, size limits, and a reason for every rejection.

## Design

### 1. Release metadata — one parse, three readers

A `nyaa.Release` (parsed from the name, attached to every `TorrentResult`) holds what the filters,
the picker and the anime page read:

| field | read from | smart default when the name doesn't say |
|---|---|---|
| `Group` | leading `[Group]` or `(Group)`, else a scene-style `-GROUP` suffix | — |
| `Resolution` | today's `ExtractResolution` (canonical tokens, [#70](../../agents/decisions/070-resolution-is-canonicalized-in-the-extractor-and.md)) | — |
| `Source` | `ExtractSource` | `web-dl`, as the ranking already assumes ([#107](../../agents/decisions/107-source-ranks-finished-series-empty-source-is-web.md)) |
| `Service` | CR, NF, AMZN, DSNP, ADN, B-Global, HIDI… | — |
| `VideoCodec` | `ExtractCodec` plus `AVC` | — |
| `BitDepth` | `10bit`, `10 bits`, `Hi10` → 10; `8bit` → 8 | — |
| `AudioCodec`, `AudioChannels` | `ExtractAudio` plus `Opus`, `EAC3`; `2.0`/`5.1` including the glued `AAC2.0` | — |
| `AudioLanguages` | `Dual-Audio`/`DUAL` → ja, en; `Multi-Audio`/`MULTi` → ja, en, more; `Dub`/`English Dub` → en; `JPN` → ja | **ja** in `c=1_2` (English-translated, no dub marker) |
| `SubtitleLanguages` | the subtitle spec's aliases; `MultiSub` → en + others unknown; `HC`/`hardsub` sets `Hardsub` | **en** in `c=1_2` |
| `Container` | `.mkv`, `.mp4`, `MKV`, `MP4` | — |
| `Version` | `v2`…`v9`, `REPACK`, `PROPER` | 1 |
| `Uncensored`, `Batch` | today's checks | false |
| `Trusted`, `Remake` | the row's class (`success`, `danger`) — read but unused today | false |
| `EstimatedMbps` | size × 8 ÷ (episodes covered × AniList `duration` × 60) | unknown: no duration, or a pack with an unknown range |

`EstimatedMbps` needs AniList's `duration` (minutes per episode), which the query does not ask for
today: one more field in the GraphQL selection. For a pack, "episodes covered" is the resolved
range ([#84](../../agents/decisions/084-a-pack-without-a-range-in-its-name-gets-its.md)); extras
inflate the size, so a pack's estimate errs high.

Every field also records **where its value came from** — the name, a smart default, the
known-groups table (§4) — so the picker and the anime page can say "assumed".

### 2. Fewer Nyaa requests — the daily sweep and the search queue

Two ways to find releases replace "search every anime with a pending episode on every check".

**The daily sweep.** When a new day starts (midnight, local time), the app reads the previous
day's uploads from Nyaa's `c=1_2` listing, newest first, page by page until it passes the start
of that day: 2–3 requests (measured above). It remembers the last upload id read.

- Each name is matched against the tracked anime with today's title matching
  ([`nyaa_match.go`](../../agents/architecture.md#srcinternalnyaa): title variants, Jaccard
  threshold, cut at the first season/episode marker), then the episode or pack is read from the
  name, then the filters run (§5).
- **Only releases that match a tracked anime and pass the name filters go further.** Everything
  else is dropped where it stands: no page is opened, nothing is stored.
- A matching release's page is opened **only when needed**: a filter needs a field the name lacks,
  or a pack's episode range must come from its file list.
- A release that matches but is not acceptable **yet** (a filter's `accept_after`, §4) is kept as
  a **held release** (name, magnet, size, tags, the anime and episodes it covers), so it can be
  accepted later without searching Nyaa again.
- Seeders read a day later have settled, so `min_seeders` judges them fairly.
- **App off for a while:** at start, the sweep reads every day missed since its last run. Proposed:
  up to 14 days back (about 35 pages); anything older goes to the search queue.
- **The cost:** a new episode is found up to about a day after release (the user's choice "for
  now"). The sweep's frequency is one setting, so reading only new uploads on every check later
  is a configuration change, not a redesign.

**The search queue.** The per-anime search (today's method) is still needed for what a day of
uploads cannot hold: an anime just added, finished shows and backlog, an episode the sweep has
not brought some days after it aired, and upgrade searches (§7). Those become entries in a
**queue** that runs on a schedule the user sets:

- a **budget per period**: at most N searches per day, week, month or year, spread evenly across
  the period;
- **time windows**: only between given hours, or on given days;
- what it holds back: the searches, and optionally the **downloads** they find (a setting; by
  default a found release starts downloading right away);
- entries are deduplicated per anime and ordered oldest first; the queue's state shows on the
  Status page (next search, budget left).

Proposed: an episode joins the queue when the sweep hasn't brought it **2 days** after it aired.
Hand actions — the picker, a manual episode download, `debug-anime` — are the user asking now and
skip the queue. The episode miss backoff
([#100](../../agents/decisions/100-an-empty-per-episode-search-waits-before-repeating.md)) keeps
applying inside the queue.

**What changes in the check.** The check still runs every `check_interval`: it reads the lists,
takes the releases the sweep accepted or the queue found, adds torrents, organizes and prunes. It
stops searching Nyaa on its own. Decision #100's "the anime search still runs every pass" is
reversed by this section and gets amended in the same change.

### 3. Lists — which anime a filter covers

Lists are made in the app (a Lists page). They are not AniList's or MAL's lists, although they are
built from them. A list has a name and any mix of:

- **anime picked one by one**;
- **whole lists from an account**: an AniList status list (Watching, Rewatching, Planning…), an
  AniList custom list, or a MyAnimeList status list (MAL has no custom lists);
- **"added by hand"**: the anime tracked without any account (standalone);
- **exceptions**: anime picked one by one that the list leaves out even when a source brings them.

```json
"lists": [
  {
    "name": "Kids — dubbed",
    "sources": [
      {"account": "anilist:second-user", "custom_list": "Kids"},
      {"account": "mal:second-user", "status": "watching"}
    ],
    "anime": [21],
    "except": [154587]
  },
  {
    "name": "Everything I watch",
    "sources": [
      {"account": "anilist:me", "status": "CURRENT"},
      {"account": "anilist:me", "status": "REPEATING"},
      {"added_by_hand": true}
    ]
  }
]
```

A filter applies to **any selection of lists**: one, several, or all of them; the selector offers
every list and a "select all". Membership is read from what each check already fetches: the list
statuses per account (`fetchListStatuses`) and AniList's custom-list flags. A source only brings
anime the app downloads at all — a Planning source matters only when Planning is in
`download_statuses`. An anime in several lists gets the filters of all of them. **An anime in no
list that has an enabled filter is downloaded as today**, with no filtering: nothing changes for an
install that never makes a list.

### 4. Filters

```json
"filters": [
  {
    "name": "Dual audio BD",
    "enabled": true,
    "lists": ["Kids — dubbed"],
    "priority": 20,
    "kinds": ["pack", "movie"],
    "resolutions": {"match": ["1080p"]},
    "sources": {"match": ["bd", "bdrip", "bdremux"]},
    "audio_languages": {"match": ["ja", "en"], "logic": "all"},
    "subtitles": {"except": ["hardsub"]}
  },
  {
    "name": "1080p, Japanese audio",
    "enabled": true,
    "lists": ["Everything I watch", "Kids — dubbed"],
    "priority": 10,
    "when_not_stated": "smart_and_groups",
    "resolutions": {"match": ["1080p"]},
    "video_codecs": {"match": ["h.264", "hevc"]},
    "audio_languages": {"match": ["ja"]},
    "subtitle_languages": {"match": ["en"]},
    "release_groups": {"except": ["somegroup"]},
    "releases": {"except": ["*hardsub*", "*[HC]*"]},
    "bitrate_mbps": {"min": 2, "max": 15},
    "size_gb_per_episode": {"max": 4},
    "seeders": {"min": 5}
  },
  {
    "name": "Anything, two weeks after airing",
    "enabled": true,
    "lists": ["Everything I watch"],
    "priority": 0,
    "accept_after": {"since_airing": "14d"}
  }
]
```

**Fields.** Every field is optional; an empty field matches anything. List fields take `match`,
`except` (wins over `match`) and, where a release can carry several values, `logic` (`"any"` or
`"all"`):

| field | values |
|---|---|
| `resolutions` | canonical tokens (`1080p`, `720p`, `2160p`…) |
| `sources` | `web-dl`, `webrip`, `bd`, `bdrip`, `bdremux`, `dvd`, `tv`, `hdtv` |
| `services` | `cr`, `nf`, `amzn`, `dsnp`, `adn`, `b-global`, `hidi`… |
| `video_codecs` | `h.264`, `hevc`, `av1`, `vp9`, `xvid` |
| `bit_depths` | `8`, `10` |
| `audio_codecs`, `audio_channels` | `aac`, `opus`, `flac`, `ac3`, `eac3`, `dts`, `truehd`, `mp3`; `2.0`, `5.1`, `7.1` |
| `audio_languages` | ISO 639-1 codes plus `multi` |
| `subtitle_languages` | the subtitle spec's closed vocabulary (`en`, `pt-br`, `es-la`, `fr`…) |
| `subtitles` | `hardsub`, `softsub` |
| `containers` | `mkv`, `mp4` |
| `release_groups` | wildcards |
| `releases` | wildcards, or Go regex with `"regex": true`, over the whole name |
| `other` | `uncensored`, `repack`, `batch`, `trusted`, `remake` |

Ranges: `bitrate_mbps`, `size_gb_per_episode`, `seeders` (`min`, `max`). `kinds`: `episode`,
`pack`, `movie` (all three when absent).

**When the name doesn't say.** A menu with four modes — one default in Settings, and every filter
can pick its own (`when_not_stated`):

| mode | what fills a field the name leaves out | what is still unknown |
|---|---|---|
| **Smart defaults + known groups** (default) | the smart defaults in §1 (Japanese audio, English subs in `c=1_2`; empty source is WEB-DL), then the known-groups table | passes |
| **Smart defaults only** | the smart defaults | passes |
| **Not stated passes** | nothing | passes: a filter only rejects what the name contradicts |
| **Not stated fails** | nothing | fails any filter that checks that field |

The **known-groups table** (Settings, editable) lists groups with one fixed pipeline. It ships
with SubsPlease = CR, WEB-DL, H.264, AAC 2.0, Japanese audio, English softsubs. It holds no group
whose output varies: the August measurement found ToonsHub's subtitle languages change with the
streaming region, so a table entry would be wrong a third of the time.

**Only accept after.** `accept_after` has two clocks, both **off by default**, set per filter in
hours or days:

- `since_airing` — the episode aired that long ago (AniList's airing time; an episode without one,
  from a finished show or the backlog, has waited enough);
- `since_upload` — the release has been on Nyaa that long (lets a `v2` or a repack replace a broken
  first upload, and seeders settle).

With both set, both must have passed. Their default values are decided after measuring how late
the releases people prefer usually arrive (dual audio, BD).

**How a candidate is judged.**

1. The filters for the anime are the enabled ones whose lists hold it. None → today's behavior.
2. A candidate's **tier** is the highest `priority` among those filters that it matches; matching
   none rejects it.
3. Candidates sort by tier, then by the existing `criteria_order` ranking within a tier. The
   Priorities page keeps its job; the ignore list stays a global "except releases".
4. A candidate whose best filter's `accept_after` hasn't passed is **held** (§2) and accepted on the
   first check after it passes, unless a higher tier arrived meanwhile. The episode reports
   `waiting_for_preferred` (§8).

The third filter above is how "prefer X, but don't wait forever" is written: a catch-all on the
same lists, low priority, accepting anything two weeks after airing.

Packs go through the same judgment — tier first, then coverage
([#79](../../agents/decisions/079-pack-choice-asks-does-it-cover-the-window-not-is.md)). A pack and a
loose episode matching different tiers: the higher tier wins the episode.
`size_gb_per_episode` divides a pack by the episodes it covers; the global
`max_batch_torrent_size_gb` still guards the disk on its own.

### 5. Where the filters run — cheap first, as autobrr does

1. **On the name**, for every field read from it: in the daily sweep, and as a row predicate in
   queue searches. A row no filter accepts must not count toward the pagination floor — the trap
   [#80](../../agents/decisions/080-the-pack-size-ceiling-is-applied-twice-in-the-nyaa.md) fixed
   for oversized packs. Held rows do count: they are valid, just not yet.
2. **In the daemon**, for what needs more than the name: `size_gb_per_episode` and `bitrate_mbps`
   (episodes covered, duration), next to today's `filterBySize`/`filterBySeeders`.
3. **The view page**, only when a filter's verdict hinges on a field the name left unknown and
   that filter sets `"resolve_unknown": true`: grep the description, an in-memory cache by Nyaa
   id, stop once `episode_retry_limit` candidates are accepted, a failed fetch counts as unknown
   and never enters the cache. At most 3 fetches per anime per check, like the pack detail
   ([#84](../../agents/decisions/084-a-pack-without-a-range-in-its-name-gets-its.md)), all through
   the request gap ([#135](../../agents/decisions/135-the-nyaa-request-gap-is-one-global-slot-held.md)).

**The manual picker** groups the candidates by the filter they match, highest priority first; a
candidate appears under its best filter. A last group, **Rejected**, holds the ones no filter
accepts, each with the field that failed. Every candidate stays selectable: a hand pick overrides
the filters, as it already overrides the size and seeder limits
([backlog](../../backlog/manual-download-skips-size-and-seeder-filters.md)). `debug-anime` prints
the same per-field verdicts.

### 6. Following a release

When you download a release by hand (the picker), **Follow this release** makes later episodes of
that anime come from the same release line. By default a later release is the same line when it
shares the **group and key tags**: resolution, source and service, audio languages. For
`[ToonsHub] Show S01E01 1080p NF WEB-DL DUAL AAC2.0 H.264` that is ToonsHub, 1080p, NF WEB-DL,
dual audio; the codec and audio codec may differ. **Advanced:** tick exactly which tags must
match.

A followed line acts as a filter of its own for that anime, above every list filter. Proposed: if
the line hasn't brought an episode **7 days** after it aired, the anime's normal filters take
over for that episode. An episode from the followed line counts as top tier for upgrades (§7).
The anime page shows the line and a "stop following" action.

### 7. Upgrades

**The user's rule: always upgrade.** An episode whose release sits below the best tier that its
filters can give keeps being looked for, and a release in a higher tier replaces it. This reverses
[#107](../../agents/decisions/107-source-ranks-finished-series-empty-source-is-web.md)'s "don't
auto-replace a saved WEB episode when a BD appears"; that entry gets amended in the same change.

- **When it stops:** once the episode holds a release from the highest-priority filter that
  applies to it (Sonarr calls this the cutoff), or from a followed line. An episode with no filters
  never upgrades.
- **How better releases are found:** the daily sweep matches its uploads against these episodes
  too, at no extra request; and every so often the anime joins the search queue (§2) to find
  better releases uploaded long before — a BD pack is rarely a recent upload. Proposed: an upgrade
  search per anime every 7 days, inside the queue's budget and windows.
- **The switch:** the better release downloads next to the old one; once it is organized into the
  library, the old torrent and its files move to the **Replaced** section. A pack replaces loose
  episodes the same way. A pack being replaced by loose episodes only goes once **all** its
  episodes are covered by better releases.
- **Replaced section** (a main page in the navigation): what replaced what, and when each old
  item will be removed. Old files are **kept for 3 days** by default, settable in hours or days.
  Actions: remove now, keep (pin the old one, which turns it into "keep both"), undo (bring the old
  one back and remove the new). The disk guard counts what waits there.
- **Approval:** a setting with three modes — **automatic** (proposed default), **approve before
  downloading** (the section lists "better release found for EP 5: old → new"; nothing downloads
  until you approve), **approve before removing** (the better one downloads; the swap waits for
  you).
- **Pin:** an episode or a whole torrent can be pinned from the anime page. A pinned item is never
  upgraded. Proposed: a torrent picked by hand starts pinned.
- **Keep both:** instead of replacing, the better release is downloaded **as well** and the old one
  stays. A setting with a per-anime override: **Replace** (default), **Keep both**, **Off**. Keep
  both means one episode can hold two releases, which `episodes.json` can't express today (one
  record per anime and episode). The record gains a list of releases, and the library holds two
  files for one episode.
- **A filter edit can start a wave:** raising a filter's priority, or adding a better one, makes
  every episode of its lists eligible. The Filters page shows "this would upgrade N episodes"
  before saving.

### 8. The report

Codes placed in the [#81](../../agents/decisions/081-report-code-precedence-is-a-cascade-not-a-set.md)
cascade by the order the stages run (the stage that emptied the list is the one reported):

- `filtered_out` — candidates existed, no filter accepted any. The detail names the field that
  rejected the most ("12 found, all without Japanese audio").
- `waiting_for_preferred` — acceptable candidates exist, but only held ones. The detail names the
  filter and the time left.
- `queued_search` — the episode waits for its turn in the search queue; the detail gives the next
  slot.

The last two are not problems for `episode_stuck`
([#128](../../agents/decisions/128-download-failed-reports-a-real-failure-once-per.md)) while they
last. `dropStats` gains the count the filters cut, the way it carries size and seeders today. An
episode waiting for an upgrade is not a problem and gets no code.

### 9. The torrents an anime downloaded

A section on the anime page, below the episode table: **one card per torrent** that brought this
anime's episodes.

**What a card shows:**

- the release name, group and a link to its Nyaa page; the episodes it covers (`EP 5`, `EP 1–12`);
- when and how it was added: the daily sweep, the search queue, a hand pick, a magnet, a
  redownload, an upgrade;
- size, the seeders and leechers when it was chosen, the Nyaa upload date;
- why it won: the filter, its lists and tier, or the followed line; its place among the candidates
  ("1st of 7");
- its state now: downloading, seeding, released ([#119](../../agents/decisions/119-disable-seeding-means-no-upload-at-all-and-a.md)),
  pinned, waiting in Replaced, replaced, removed;
- **from the name:** chips for resolution, source + service, video codec, bit depth, audio codec
  and channels, audio languages, subtitles — an assumed value says so;
- **from the file**, once downloaded: per video file, the container, video codec, resolution,
  duration and real bitrate; every audio track (language, codec, channels, title); every subtitle
  track (language, format ASS/SRT/PGS, default/forced). A pack shows one summary row and expands
  to its files;
- a warning chip where the file contradicts the name: "Dual-Audio" with one audio track, a language
  missing, a different resolution. It only informs.

**Where it comes from.** A new `releases` file in the data dir: a JSON map keyed by info hash (a
pack is one torrent behind many episode records, so `episodes.json` is the wrong place).

- **At add time**, every add path writes the record from the candidate it added: the sweep, the
  queue, the manual download, the picker, a magnet (name from `dn`, no Nyaa data), a redownload
  ([#140](../../agents/decisions/140-redownloading-missing-files-adds-the-same-torrent.md), which
  keeps the existing record), an upgrade.
- **After the download**, the `organize` job reads each video file's track list with a **Go EBML
  library** (the user's choice; EBML is the binary block format inside `.mkv`). Candidates to vet
  at implementation — license, maintenance, and that it can stop after the Tracks element instead
  of reading the file: `github.com/remko/go-mkvparse`, `github.com/at-wat/ebml-go`. MP4 waits until
  someone needs it. In direct mode with staging
  ([#141](../../agents/decisions/141-staged-downloads-are-copied-into-the-library-while.md)) it reads
  the library copy once it has moved.
- **Removed torrents**: a setting decides whether their cards stay, marked removed (with what
  replaced them), or disappear with the torrent.
- **Torrents from before the feature** get a card from what the session still knows (its name and
  size) and "added before release details were kept". The file read still works for them.

**API and CLI**, shipped together (CLI parity): `GET /api/v1/animes/{id}/releases` and
`autoanimedownloader anime releases <id>`; pins: `POST/DELETE /api/v1/releases/{hash}/pin` and
`autoanimedownloader torrents pin|unpin <hash>`; the Replaced section's list and actions, and the
lists, filters and queue, each with its endpoint and command.

### 10. Pages and settings

- **Lists** page: make, rename and delete lists; add sources (pick an account, then one of its
  status or custom lists), anime and exceptions; see which anime a list holds right now.
- **Filters** page, next to Priorities: the filters (name, lists, priority, enabled), an editor
  grouped like autobrr's tabs — Quality (resolution, source, service, codecs, bit depth), Audio &
  subtitles, Release (groups, name match/except, other), Limits (size per episode, bitrate,
  seeders, accept after) — and a "test against a search" box that runs a search for a chosen anime
  and shows the picker's grouped view.
- **Replaced** page (main navigation): replaced items waiting for removal, upgrades waiting for
  approval, and their actions.
- **Settings:** the default "when the name doesn't say" mode; the known-groups table; the sweep's
  frequency (once a day); the search queue's budget, windows and whether it also holds back
  downloads; the upgrade mode (Replace / Keep both / Off), the approval mode, how long replaced
  files are kept; whether removed torrents keep their cards.
- **Status page:** the last sweep (when, pages read, releases matched) and the queue (next search,
  budget left in the period).
- **Anime page:** which lists and filters apply to it, a followed line; the per-anime upgrade
  mode; pin and unpin on episodes and torrent cards.

## Delivery

Each step is usable alone:

1. **What was downloaded** — `nyaa.Release` parsing (with the extractor fixes below), the
   `releases` store written at every add, the `.mkv` track read in `organize`, the anime page
   section with its history setting, the API and CLI. No change to what gets picked.
2. **Lists and filters** — lists, filters, the "doesn't say" modes and the known-groups table,
   judgment and tiers, the name predicate, the daemon stage, AniList `duration` for bitrate,
   `filtered_out`, the Lists and Filters pages, the grouped picker, `debug-anime` verdicts.
3. **Fewer requests** — the daily sweep, held releases, the search queue with its budget and
   windows, `queued_search`, the Status page, the amendment to #100.
4. **Waiting and following** — `accept_after` (both clocks), `waiting_for_preferred`, following a
   release.
5. **Upgrades** — the cutoff, upgrade matching in the sweep and the queue, replace, the Replaced
   page with its approval modes, pins, then keep both (the `episodes.json` change), and the
   amendment to #107.
6. **Looking further** — view-page lookups for unknown fields.

## Out of scope

- The subtitle-language spec's `c=1_3` search (stays in that spec).
- Picking tracks inside a file, or remuxing: the media server's job.
- Downloading only some files of a pack: [backlog](../../backlog/download-part-of-a-pack.md).
- Sources other than Nyaa: the parsing is release-name parsing, which the
  [source-as-descriptor](2026-09-13-source-as-descriptor-design.md) split keeps on the app side, so
  filters survive it unchanged.

## Small bugs found while measuring

Planned for step 1, where the release parsing is rewritten:

- `AVC` is not read as H.264 (`reCodecPatterns`). Erai-raws writes `[1080p CR WEB-DL AVC AAC]`, so
  its codec is unknown today and ties every other release on the codec criterion.
- `Opus` is not in `reAudioPatterns`.
- `extractFansub` reads only the `[Group]` capture: for a name starting with `(Group)` it returns an
  empty group, because that alternative fills the second capture.
- Scene-style names (`… H.264-VARYG`, 14% of the sample) get no group at all.

## Decided (2026-11-23)

| # | Question | The user's answer |
|---|---|---|
| 1 | Filter model | **Several filters with priorities**, autobrr-style |
| 2 | When the name doesn't say | **All four modes in a menu**: a default in Settings, and per filter |
| 3 | Waiting | **Yes**, per filter; then: **only accept after N**, off by default, with **both clocks** (since airing, since upload); defaults decided after measuring |
| 4 | Upgrades | **Always upgrade**, plus **pin** (keep what's downloaded) and **keep both** (download the better one as well) |
| 5 | Reading `.mkv` tracks | **A Go EBML library** |
| 6 | Cards for removed torrents | **A setting** |
| 7 | Manual picker | **Grouped by the filter they match**, best first; rejected ones last and still selectable |
| 8 | Which anime a filter covers | **Lists made in the app** from anime picked one by one and AniList/MAL lists (custom lists included); a filter applies to **any selection of lists**, one, some or all |
| 9 | The August subtitle-language spec | **Stays separate** |
| 10 | Nyaa requests | **Read the previous day's uploads once a day** for now; only releases matching the lists and filters go further; pages opened **only when needed** |
| 11 | Per-anime searches | **A queue** with a **budget per period** (day, week, month, year) **and time windows**; it can hold back **searches and downloads**, downloads start right away by default |
| 12 | Following a release | **Group and key tags**, with an **advanced per-tag override** |
| 13 | Finding upgrades | **Daily uploads, plus the queue** |
| 14 | Replaced files | Kept **3 days** by default, settable **in hours or days**, listed in a **main section** |
| 15 | Upgrade approval | **A setting**: before downloading or before removing (plus automatic) |

## Proposed defaults — say if you disagree

- Upgrades are **automatic** until you turn approval on.
- A torrent picked by hand starts **pinned** (no upgrade replaces your own choice).
- An episode the sweep hasn't brought **2 days** after airing joins the search queue.
- A followed line hands over to the normal filters **7 days** after airing.
- Each anime gets an upgrade search through the queue every **7 days**.
- After downtime, the sweep catches up **14 days** at most; older goes to the queue.
- Hand actions (picker, manual download, `debug-anime`) **skip the queue**.
- The known-groups table ships with **SubsPlease only**.
- **Keep both** names the second file with its release tags so the media server can tell them
  apart; how Jellyfin shows two files for one episode gets checked before step 5.

## Still open

- **More conditions**: you said more are coming; they join the field table.
- **`accept_after` defaults**: after measuring how late dual-audio and BD releases arrive.
