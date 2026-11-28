# 163. A megapack is a torrent whose files map onto two entries of a franchise group, and a group entry needs both an AniList relation and a similar name

**Location:** `internal/anilist/franchise.go` — `GetFranchise` (the walk over `franchiseRelations`,
`SimilarFranchiseTitles` as the gate of `admit`); `internal/daemon/megapack.go` — `MapMegapack`
(creditless → special-marked → series axis), `specialMatcher.match`, `seriesSpans`;
`internal/nyaa/megapack.go` — `MegapackSignals`, the file-list cache (`SetFileListCacheDir`,
`cachedFileList`, `storeFileList`), `IsSpecialMarked`. Tests:
`TestGetFranchise_WalksPastTheCutAndNeedsRelationAndName`, `TestSimilarFranchiseTitles`,
`TestMapMegapack_*`, `TestMegapackSignals`, `TestFileListCache_PackRangeReadsWhatTheDetailRead`,
`TestFileListCache_DoesNotKeepAnEmptyList`.

**What it looks like:**
- the name already says "S01-05" or "+Movies", yet the picker asks AniList and reads the file list
  before calling the torrent a megapack;
- the franchise walk follows seven relation types and then throws away entries that the relations
  reached;
- a file list is written to disk and never refreshed, while every other Nyaa read is fresh;
- a movie is matched by the words its title does *not* share with the franchise, which reads
  backwards.

**Why it's right:** this is step 1 of the megapack spec
(`docs/superpowers/specs/2026-11-23-megapack-downloads-design.md`), decided with the user on
2026-11-23 (rows 3, 4, 5 of its table) and 2026-11-26 (map a franchise pack's episodes to every entry
it carries).

- **The name only proposes.** Of the 133 megapacks measured, the names disagree on what a megapack
  is: `S01-05`, `Seasons 1-4 + OVAs`, `(0001-1071+Movies+Specials)`, `Complete Batch`, and Bleach's
  `001 ~ 366`, which is one AniList entry. `MegapackSignals` drives the chip on the picker row; the
  mapping says `megapack` only when files land in two entries or more.
- **Relations and names, both.** AniList has no franchise id ([#71](071-anilist-has-no-franchise-id-the-prequel-chain-is.md)).
  Names alone join unrelated works (`Monogatari` and `Ore Monogatari!!`). Relations alone drift: a
  `SPIN_OFF` of a `SPIN_OFF` is another work, and `Chuukou Ikkan!! Kimetsu Gakuen Monogatari` is a
  Kimetsu spin-off no megapack carries. So an entry joins only when a followed relation reaches it
  **and** its romaji or English title shares with the seed's either the whole shorter name
  (`Bleach` in `Bleach: Sennen Kessen-hen`) or a run of 8 letters (`monogatari` in `Bakemonogatari`
  and `Nisemonogatari`). A node that fails is not walked from, which is what stops the drift.
  Synonyms are not compared: AniList's synonyms carry other languages and nicknames. `ADAPTATION`,
  `SOURCE`, `CHARACTER` and `OTHER` are never followed (manga, novels, crossovers). The walk reads
  two levels per request and sends the cut level back to the frontier, like the series walk
  ([#77](077-the-per-series-absolute-axis-is-a-bfs-two.md)), capped at 8 rounds and 120 entries.
- **Files onto entries.** TV episodes use the series axis that packs already use (#77,
  [#78](078-a-torrents-unit-of-ownership-is-coverage-on-the.md)), with restarting season folders
  laid end to end in season order ([#116](116-restart-per-season-packs-flatten-onto-the.md)); season
  folders named in words (`First Season`, Haikyuu) are read too. A movie, OVA or special is placed
  by the title words that are not shared by most of the group: `One Piece Film Red` and
  `One Piece Film Gold` share `one piece film`, so only `red` and `gold` can tell them apart. The
  year is evidence (`JoJo OVAs [1993]` against the 2000 OVA); the format word counts strongly only
  when the group has one entry of that format. A tie, or a score under half, leaves the file
  unmapped: the spec's rule is "listed, never guessed". Creditless openings, menus and promos are
  unmapped first.
  The torrent's own top folder is not read for markers: a multi-file torrent always has one, named
  after the release, and `Anime (S1 + OVA + Specials)` would make every episode a special. On a file
  name only tags that no episode title carries count (`OVA`, `S00E`, `.5`, `SP03`; `Movie` only on
  an unnumbered file), since `EP 03 - Special Training!` is an episode
  ([#113](113-packfilerange-min-max-skips-extrasova-an-isolated.md)).
- **File lists are kept forever.** A torrent's files never change (the info hash fixes them), and
  One Piece's 1,509-file page is 175 KB. A list is kept in `nyaa-files/<id>.json` in the data
  folder: the picker detail writes it (it reads the page anyway, for the description, which
  uploaders edit), and `PackFileRange` reads it, so the pass's pack-range reads stop costing a
  request after the first. An empty list is never kept, since a page that briefly lost its list
  would read as an empty torrent forever.
- **The group is cached a day, an hour when it is still open.** Only the picker reads it, and a
  stale count only shifts that table. A group with an airing or uncounted entry keeps for an hour,
  closer to the series axis's rule of never caching such a node (#77). When the pass reads the group
  (spec step 3) it must follow #77's rule.
- **The pass doesn't use any of it.** Step 1 is "no new download behavior": the mapping is shown,
  never acted on, and the pack path still drops a season-range pack for a later season
  ([#83](083-the-pack-names-range-is-the-first-plausible-match.md)), the user's hold of 2026-11-26.
  Taking `S01-05` for season 3 would download the whole franchise until file selection (spec
  step 2, qBittorrent's file priorities) can skip the seasons nobody tracks.

Trade-off accepted: restarting seasons with a missing last episode shift the next season's offsets,
the same limit `PackFileRange`'s flattening has.

**Amendment (see [#165](165-a-torrents-files-are-chosen-with-qbittorrents-file.md)):** file
selection (spec step 2) is built for the picker and the Downloads file panel, qBittorrent only:
the mapping now decides the picker's default boxes (everything it places ticked, its unmapped
videos not), and the add reads the cached list (`nyaa.TorrentFileList`) for the selected size and
the episodes left with no file. The pass still takes whole packs and doesn't read the mapping, so
the season-range hold (#83) stays until the pass chooses files itself (spec step 3, with
"Download related seasons").

**Don't "fix" by:**
- Calling a torrent a megapack from its name alone: `001 ~ 366` is one entry, and `Complete Batch`
  is often one season.
- Dropping the name check from the franchise walk "because the relations are authoritative": a
  long spin-off chain pulls in other works, and the group is what the mapping matches movies
  against.
- Comparing a candidate with every member instead of the seed: similarity chains (A like B, B like
  C) drift the same way relations do.
- Refreshing the file-list cache on a timer: the list can't change, and a refresh only costs Nyaa
  requests.
- Reading the torrent's top folder for movie or OVA words: the release name describes the whole
  torrent.
- Letting the pack path keep a season-range pack before file selection exists: one season's entry
  would download a franchise.
- Matching movies on the whole title: every One Piece movie would tie on `one piece`.
- Mapping `Season N` folders to the Nth AniList entry: AniList splits seasons into cours (Attack on
  Titan's Season 3 is two entries), so the axis decides, not the folder's number.
