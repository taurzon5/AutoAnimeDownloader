# Direct library mode: no hardlinks, Series/Season layout, packs as-is, several download roots

**Date:** 2026-11-22
**Status:** PR 1 (direct mode, one root) merged. PR 2 (several roots) merged too (decisions/154), without the per-root swap marker and `series_drive_first`, which waits for PR 3's rules. PRs 3–4 (rules and the movie library, pack season rename) not started — no fixed release
**Origin:** a user who seeds without limit wants the library file to *be* the seeding file (no
`.torrents` copy, no hardlink, so filesystems without hardlinks work too), episodes filed as
`<Series>/Season 01/`, multi-season packs left in their own layout, and torrents spread across
several drives chosen by rules.
**Scope:** `internal/torrents` (a rain `storage.Provider`, placements), `internal/files` (config,
`AnimeSettings`, Librarian in direct mode), `internal/daemon` (placement at add time, organize,
deletion, disk budget, health), `internal/anilist` (series root title, year, studios, director),
`internal/api` (config validation, per-anime setting, status disks), CLI (`animes settings`),
`frontend` (Config group, per-anime setting, Status disks, i18n). No new endpoint.

---

## The problem

Today every torrent downloads to `<completed_anime_path>/.torrents/<infohash>/` and, once
complete, its video files are hardlinked into `<completed_anime_path>/<Entry Name>/`
([decisions/021](../../agents/decisions/021-embedded-torrent-client-hardlink-into-library.md),
[/031](../../agents/decisions/031-download-directory-is-derived-from-the-library.md)). That model has
four consequences this user does not want:

- **It needs hardlinks.** exFAT, FAT32 and many SMB/NFS shares are refused at config save
  (`Librarian.ProbePath`).
- **It needs one volume.** The download folder lives inside the library, so the whole collection
  must fit on one drive.
- **One folder per AniList entry**
  ([decisions/045](../../agents/decisions/045-one-library-folder-per-anilist-entry-the-season.md)):
  Mushoku Tensei becomes four sibling folders instead of one series with seasons.
- **Packs are sliced.** A multi-season pack is cut per entry and its other seasons wait for their
  entries to reach the watch list
  ([decisions/116](../../agents/decisions/116-restart-per-season-packs-flatten-onto-the.md),
  [/117](../../agents/decisions/117-off-list-bypasses-the-unclaimed-pack-guard.md)).

## What this is not

- **Not a replacement.** The hardlink mode stays the default and keeps every current decision.
  Direct mode is opt-in and only changes torrents added while it is on.
- **Not a mover.** A torrent's place is fixed when it is added and frozen while it seeds. Changing a
  rule, a root or the mode later only affects new torrents. Moving existing torrents is a separate
  feature.
- **Not per-file selection inside a pack.** rain still downloads the whole torrent
  ([download-part-of-a-pack](../../backlog/download-part-of-a-pack.md)).
- **Not a Jellyfin metadata fix.** The `Season NN/` layout reintroduces what decisions/045 left:
  with `jellyfin-plugin-anilist`, every season of a series folder shows the first entry's metadata.
  The settings page says so next to the mode switch. TVDB/TMDB-style providers are fine.

---

## Settings

All in `config.json`, all settable with `config set` (no new CLI command for these).

| JSON key | Type | Default | Meaning |
|---|---|---|---|
| `library_mode` | `string` | `"hardlink"` | `"hardlink"` = today. `"direct"` = this spec |
| `download_roots` | `[]DownloadRoot` | `[]` | Direct mode only. `{"name": "ssd", "path": "/mnt/ssd/Anime"}`. Empty means one root, `completed_anime_path`, named `"library"` |
| `folder_rules` | `[]FolderRule` | `[]` | Direct mode only. Ordered; see [Choosing a root](#choosing-a-root) |
| `rename_pack_season_folders` | `bool` | `false` | Direct mode only. A pack's season folders become `S01`, `S02`… under the series folder |
| `auto_delete_downloads` | `bool` | `false` | Direct mode only. `false` = keep forever: no automatic path deletes a direct-mode torrent, and `max_episodes_per_anime` is ignored (see [Deletion](#organize-and-deletion-in-direct-mode)). `true` = today's rules (`delete_watched_episodes`, `delete_statuses`, blacklist, off-list, the episode limit) apply, and deleting stops the seed |
| `incomplete_files` | `string` | `"part"` | Direct mode only. `"part"` = a file is written as `<name>.part` and renamed to its real name when the torrent completes, so the media server never sees a half-written episode. `"in_place"` = written under its final name from the start |
| `series_drive_first` | `bool` | `false` | Direct mode only. `false` = `folder_rules` decide first and the drive already holding the series only breaks a tie. `true` = the series' drive wins over the rules, so later seasons land next to earlier ones |
| `movies_root` | `string` | `""` | Direct mode only. `""` = off: movies go to `<Series>/Specials/`. A path = a separate movie library in Jellyfin's movie layout, `<movies_root>/<Title (Year)>/<Title (Year)>.<ext>` |
| `episode_numbering` | `string` | `"season"` | Direct mode, with `rename_files_for_jellyfin`. How a split cour (Season 2 Part 1 and Part 2, separate AniList entries each numbered from 1) is named inside its shared `Season 02/`. `"season"` = continue the season's numbering, TVDB/TMDB style: Part 2 episode 1 becomes `S02E13`. `"entry"` = keep each AniList entry's own numbers, AniList style, with a part tag so they don't collide: `S02E01 (Part 2)` |

`completed_anime_path` stays required (`isConfigComplete`). In direct mode it is root 1 when
`download_roots` is empty, and the place `tvshow.nfo` backfill and the leftover sweep start from.

Per anime, `AnimeSettings` gains `root *string` (a root name): the override. The PUT stays partial
(pointer field), and CLI parity means `animes settings <id> root <name|none>` plus a
`Client.UpdateAnimeSettings` that takes the new field.

Validation (`handleUpdateConfig`): `library_mode` is one of the two values; root names are unique
and non-empty; root paths are absolute, distinct, not nested in each other, and writable (a small
probe file, not a hardlink); every rule names an existing root. `ProbePath` (hardlink probe) runs
only in hardlink mode.

---

## Layout

### Series name and season number

- **Series name:** the title of the root of the PREQUEL chain (`anilist.Series.Key`), using the
  same title choice as `AnimeName`. `seriesNodeFields` has no title today: add `title{romaji
  english}` to it (the walk already fetches those nodes, so no extra request).
- **Season number:** walk the chain root first. An entry's own season marker wins ("Season 2",
  "2nd Season", "II"); an entry without one continues its prequel's season when it is a later part
  ("Part 2", "Cour 2", [decisions/010](../../agents/decisions/010-cour-n-treated-as-part-n-not-as-a-distinct-concept.md)),
  and starts the next season otherwise; the root without a marker is season 1. The anime itself
  also reads its synonyms; ancestors only their titles. Unknown chain: the anime's own marker, then
  `1`. Details and limits:
  [decisions/138](../../agents/decisions/138-direct-mode-numbers-seasons-along-the-tv-prequel.md).
- **The folder is pinned:** a series that already has a folder (any placement with the same series
  id) keeps it, even after AniList renames the root.
- Both are computed once, when the torrent is added, and stored on the placement (below). AniList
  correcting a relation later never moves a seeding file.

### Where files go

| Torrent | Path |
|---|---|
| Single episode (one video file) | `<root>/<Series>/Season NN/<file>`; with `rename_files_for_jellyfin` the file becomes `<Series> - SNNEMM.<ext>`, numbered per `episode_numbering` |
| Movie (AniList format `MOVIE`), `movies_root` off | `<root>/<Series>/Specials/<file>`: Jellyfin's Season 00, where it files a movie that belongs to a series |
| Movie, `movies_root` set | `<movies_root>/<Title (Year)>/<Title (Year)>.<ext>`. Title is the movie's own AniList title, year its `seasonYear`. Only the main video (the largest) is renamed; other files keep their names in the same folder |
| Pack (any multi-file torrent) | `<root>/<Series>/<torrent folder>/…` exactly as the torrent lays it out |
| Pack with `rename_pack_season_folders` | each season group → `<root>/<Series>/S0N/<file>`; files in no group stay under `<root>/<Series>/<torrent folder>/` |

Season groups are read exactly as `PackFileRange` already groups a restart-per-season pack
(`S01E05` in the name, else the parent folder, decisions/116). A one-season pack is still "left as
is"; with the rename on, its one group becomes `S0N` where N is the entry's season.

`tvshow.nfo` goes in `<root>/<Series>/` with the chain root's id, never overwritten
([decisions/044](../../agents/decisions/044-tvshownfo-is-written-after-the-hardlinks-never.md)).
One series spread over two roots gets one nfo per root.

**A pack shared by two entries no longer needs two folders.** Under the series layout S1 and S2
live in the same series folder, so adoption
([decisions/078](../../agents/decisions/078-a-torrents-unit-of-ownership-is-coverage-on-the.md))
just records the same paths for the second entry. That removes the one thing direct mode could not
do without a copy.

---

## Storage

rain v2.5.1 exposes `torrent.Config.CustomStorage storage.Provider`. Direct mode plugs one in
(`internal/torrents/placement.go`), built in `rainConfig` and rebuilt with the session.

- **Placement registered before `Add`.** rain calls `GetStorage(id)` synchronously inside `AddURI`,
  before metadata and before the episode record exists. So the daemon writes a placement first:
  `placements.json` in the data dir, keyed by infohash (rain's torrent id is already the infohash,
  `Session.Add` passes `ID: hash`). A placement holds root name, root path, series name, season,
  kind (episode / movie / pack), the per-file rename flags, and later the frozen file map.
- **`Prepare(ctx, Manifest)` freezes the file map.** rain hands over the full file list (paths as
  `<torrent name>/<inside path>`) before opening anything. The provider computes each destination
  once with the layout rules above, refuses a destination outside the root or one already used by
  another placement, and saves the map. `Open(name)` only looks names up in that map. A restart
  reads the same map, so seeding never moves.
- **`RootDir()`** returns the series folder; `TorrentInfo.DataDir` then means "where this
  torrent's files are". Readers that join `DataDir` with a raw torrent path (the torrents panel's
  `videoCodecOf`) must go through the map instead.
- **`Remove(m, keepData)`** (`storage.Remover`) deletes the mapped files and the folders it created
  that are now empty, only when `keepData` is false. Without it rain deletes nothing in custom
  storage.
- **No placement for an id** (a torrent added in hardlink mode, or before the switch): the provider
  falls back to `<DownloadPath()>/<id>`, today's layout, so turning the mode on never breaks a seed
  in progress.
- **Partial files** (`incomplete_files`). The `.torrents` dot-folder used to hide them. With
  `"part"`, the frozen map holds two names per file, the `.part` one and the final one, plus a
  `completed` flag on the placement. `Open` uses the `.part` name until the torrent completes; on
  completion the daemon closes the torrent's files (stop), renames every `.part` to its final
  name, sets `completed`, and starts it again to keep seeding. On a restart the flag says which
  name is on disk. With `"in_place"`, the map holds only the final names.

The folder-swap marker (`.aad_root`, [decisions/034](../../agents/decisions/034-a-download-folder-swap-is-detected-by-a-dual.md))
moves to each root: one marker and one id per root, and `library_missing` names the root that
vanished. A vanished root clears `LibraryPaths` only for torrents placed there.

---

## Choosing a root

At add time, for one torrent, in this order; the first that yields a root **with space** wins
("space" is today's disk guard, per root):

1. **Per-anime override** (`AnimeSettings.root`).
2. **Series drive**, only when `series_drive_first` is on: the root already holding files of this
   series (same `Series.Key`), so `Season 02/` lands next to `Season 01/`.
3. **`folder_rules`**, first match. A rule is `{"root": "<name>", "match": {...}}` with any of:
   - `year`: `{"from": 2000, "to": 2015}` on AniList `seasonYear` (fallback `startDate.year`);
   - `studio`: list of names, case-insensitive, matched against the main studios;
   - `director`: list of names, matched against staff with role `Director`;
   - `codec`: list of tokens from `nyaa.ExtractCodec` on the chosen release name (`hevc`, `av1`, `h.264`…);
   - `kind`: `episode`, `pack`, `movie`.
   All keys in one rule must match (AND); rules are tried in order (first match).
4. **Series drive as a tie-break** (when `series_drive_first` is off): no rule matched, so the root
   already holding this series is preferred.
5. **Fill in order:** `download_roots` in list order, first with space.

No root has space → the add is refused with the same `disk_low` / `blocked` path as today. A movie
with `movies_root` set skips this list: it goes to `movies_root`, which gets its own disk guard.

**Data this needs.** The pass query (`GetAllCurrentAnime`) gains `seasonYear startDate{year}
studios(isMain: true){nodes{name}}`, and `staff` only when a rule uses `director` (it is the heavy
field; check against the AniList budget,
[decisions/072](../../agents/decisions/072-anilist-budget-is-measured-from-the-response.md)).
`attemptDownloadWithRetries` and `ManualDownloadEpisode` must hand the chosen candidate's **name**
to the placement step (today only `(hash, size)` leaves it). Magnet-only adds use the magnet's `dn`;
with no name the `codec` rule does not match.

---

## Disk

- `checkRootBudgetSpace`, `newDiskBudget` and `DiskLow` work **per root**: reservations of incomplete
  torrents are counted against the volume their placement is on.
- `GET /status` reports one disk entry per root (`disks: [{name, path, total, free, low}]`); the
  current single `disk_total`/`disk_free` stay as the sum for older clients.
- `disk_low` alerts name the root.
- `librarySize` (`health.go`) stops assuming the library shares bytes with a seeding copy: in direct
  mode the library file is the only copy.

---

## Organize and deletion in direct mode

**Organize** (`organizeTorrent`) no longer links anything: the files are already in place. It
records `LibraryPaths` from the frozen map (video files only, as today), writes `tvshow.nfo`,
clears stuck state and notifies. `reconcileLibrary`, adoption and the partial-group rule keep
working because they only read and write `LibraryPaths`.

**Deletion.** In direct mode the library file and the torrent data are the same bytes, so
`removeEpisodesAndLinks` must not call `RemoveFromLibrary` and then `Remove(hash, false)`: it calls
only `Remove(hash, keepData)` and the provider deletes.

| Path | `auto_delete_downloads: false` (keep forever) | `true` |
|---|---|---|
| Watched pruning, `delete_statuses`, blacklist, off-list | no-op for direct-mode torrents | today's rules, guarded by #74/#117; the torrent and its files go |
| `max_episodes_per_anime` | **ignored**: every aired episode downloads and nothing is deleted for being over the limit. Like `0` today, this also opens the pack window (`windowEnd`), so the brakes left are `max_batch_torrent_size_gb` and the disk guard | today: caps how many unwatched episodes are downloaded ahead of progress, and deletes unwatched non-pack episodes above it |
| Manual delete (episode, batch, torrent) | removes torrent and files; `keep_data` keeps the files and drops the torrent from the session | same |
| `disable_seeding` | the torrent leaves the session with `keepData=true`; files stay in place | same |
| Failed / stalled drop | today's `keepData` rule; a retry with a **different** infohash deletes the abandoned placement's partial files | same |

`deleteEmptyFolders` learns the two-level layout (`<Series>/Season NN/`) so an emptied season folder
and then an emptied series folder are swept.

---

## Decisions this touches

They keep holding in hardlink mode. Direct mode gets its own entries, and each of these gets an
amendment that scopes it to hardlink mode:

019 (disk stat on one volume), 021 (hardlink into library), 022 (organize into
`completed_anime_path`), 026 (hardlink probe every pass), 028 (organize replaces same-name file),
029 (startup reconciliation), 031 (download dir derived from library), 034 (one swap marker), 044
(nfo location), 045 (one folder per entry), 106 (nfo sweep), 116 (organize slices by entry), 119
(disable seeding deletes the download copy).

New entries to record when implemented: the placement is frozen at `Prepare` and never recomputed;
direct-mode deletion goes through the provider only; series affinity before rules.

---

## Settings page

A new advanced group, **Library layout**: the mode switch (with the Jellyfin/AniList-plugin
warning), the roots list, the rules list (one row per rule, root picker plus match fields),
"series drive first", the movie folder, partial-file handling, split-cour numbering, the
pack-folder rename, and keep-forever vs auto-delete (saying that keep forever also turns off the
episode limit). Everything except the mode switch is hidden in
hardlink mode. The anime detail screen gets a root picker for the override. Status shows one disk
card per root. All strings in `en.json` and `pt-BR.json`.

---

## Delivery

Four PRs, each usable on its own:

1. **Direct mode, one root:** placement provider, Series/Season layout, packs as-is,
   `incomplete_files`, `episode_numbering`, movies in `Specials/`, keep-forever switch,
   organize/deletion changes, decisions. The core and the largest.
2. **Several roots:** `download_roots`, fill in order, `series_drive_first`, per-anime override
   (with CLI), per-root disk guard and status, per-root swap marker.
3. **Rules and the movie library:** `folder_rules`, the AniList fields (`seasonYear`, studios,
   staff), passing the release name to placement, and `movies_root` (it needs the year).
4. **Pack season rename:** `rename_pack_season_folders`.

## Tests

- Provider: map frozen at `Prepare`, identical after a session rebuild; `Open` of an unmapped name
  fails; two placements cannot claim one path; `Remove` deletes only mapped files and only when
  `keepData` is false; unknown id falls back to `<DownloadPath>/<id>`.
- Layout: single episode, Jellyfin rename, movie in `Specials/` and in `movies_root`, continuous
  pack, restart-per-season pack (Kaguya EMBER), pack with the rename on, extras staying under the
  pack folder, a split cour under both `episode_numbering` values.
- Partial files: with `"part"` the `.part` names until completion, the rename on completion, the
  final names after a restart (both before and after completion); with `"in_place"` no rename.
- Root choice: each step of the order with `series_drive_first` off and on, a full root falling
  through, no root with space refusing, a movie going to `movies_root`.
- Deletion matrix above, both values of `auto_delete_downloads`, including the episode limit
  being ignored under keep forever.
- Golden set unchanged in hardlink mode (it runs on `DefaultConfig()`).

## Settled questions

Decided with the user on 2026-11-22; each is now in the sections above.

1. **Partial files:** both behaviors, as `incomplete_files`; `.part` + rename is the default.
2. **Series drive vs rules:** rules first by default; `series_drive_first` puts the series' drive
   ahead of the rules.
3. **Movies:** `<Series>/Specials/` by default; an optional `movies_root` (off by default) files
   them as `Title (Year)/Title (Year).ext`.
4. **`max_episodes_per_anime` with keep forever:** ignored. Download everything aired, delete
   nothing.
5. **Split cours in one season folder:** an option, `episode_numbering`: continue the season's
   numbering (`S02E13`, default) or keep the AniList entry's numbers with a part tag.

## Open questions

None left from the first review. The default of `episode_numbering` (`"season"`) was chosen in
the spec, not by the user: it matches the `Season NN/` layout and TVDB/TMDB numbering.
