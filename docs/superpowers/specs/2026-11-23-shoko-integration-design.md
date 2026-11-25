# Shoko integration: what you already have counts, and only its upgrades are looked for

**Date:** 2026-11-23
**Status:** step 1 (read-only matching and the sync), Download Full Megapacks (§3) and the
downloading into Shoko's own import folders (§7), implemented and merged into master
(decisions 146 to 149, 152 to 154); steps 2–3 wait for the release filters. The
API below was measured against a real Shoko Server v5.3.3 on 2026-11-24 (see "Measured against
v5.3.3"); the user's decisions are recorded at the end
**Origin:** "design a spec that takes shoko anime entries and then compares to the series we are
looking to download, if they exist in shoko, get details and look for upgrades, no downloads for
upgrades by default, if they are good, show that they already exist in shoko group and are a match
for now".
**Related:** [release-filters](2026-11-23-release-filters-design.md) (filters and tiers, the cutoff,
upgrades, the approvals on the Replaced page, the downloaded-torrents section),
[megapack-downloads](2026-11-23-megapack-downloads-design.md) (franchise groups).
**Expected scope:** a new `internal/shoko` client, `internal/daemon` (matching, "owned in Shoko"
episodes, the upgrade check), `internal/files` (config, the match cache), `internal/api` +
`cmd/cli`, frontend (settings, the anime page, the Status page).

---

## What Shoko is, and why it matters here

[Shoko Server](https://shokoanime.com) is an anime library manager: it hashes the files in its
folders, identifies each one against AniDB, groups series into franchises, and feeds Jellyfin,
Plex and Kodi through its plugins (Shokofin for Jellyfin). Someone who runs it already has a
library this app knows nothing about. Today the app would download episodes that sit in Shoko
already.

## What Shoko's API gives (v5.3.3, the current stable release)

Read from the `v5.3.3` tag of `ShokoAnime/ShokoServer` on 2026-11-23 (`Shoko.Server/API/v3`,
`Shoko.Server/API/v0/Controllers/AuthenticationController.cs`). The master branch is moving to a
new major version with new release-info controllers, so the client targets the v3 API of the
stable line.

| need | endpoint | what comes back |
|---|---|---|
| a key | `POST /api/auth` with `user`, `pass`, `device` → `apikey`; or `POST /api/auth/apikey` (`device`) for a signed-in user | an API key, sent on every request (`apikey` header) |
| every series | `GET /api/v3/Series?page=&pageSize=` (≤ 100 per page) | per series: `IDs.AniDB`, `IDs.MAL` (a list), `IDs.TMDB`, `IDs.TvDB`, `IDs.ParentGroup`, `IDs.TopLevelGroup`, titles, sizes |
| one series' files | `GET /api/v3/Series/{id}/File` (≤ 1,000 per page, `include=` for extra data) | per file: `Size`, `Resolution`, `Duration`, `Locations` (paths), episode cross-references, `_AniDB` and `MediaInfo` |
| a group | `GET /api/v3/Group/{id}`, `GET /api/v3/Group/{id}/Series` | the franchise group Shoko built (from AniDB relations) and its series |

Per file, the two blocks that make a quality judgment possible:

- **`_AniDB`** (the release as AniDB knows it): `ReleaseGroup`, `Source` (TV, DVD, Blu-ray, web…),
  `Version`, `IsCensored`, `AudioLanguages`, `SubLanguages`, `OriginalFileName`, `ReleaseDate`.
- **`MediaInfo`** (read from the file itself): container, duration, overall bitrate, and every
  stream — video (codec, profile, width, height, bit depth, frame rate, HDR), audio (codec,
  channels, language, title, default/forced), subtitles (codec ASS/SRT/PGS, language,
  default/forced).

### Measured against v5.3.3 (2026-11-24)

These were measured against `shokoanime/server:v5.3.3` in a throwaway container, with one series
fetched from AniDB through Shoko (`POST /api/v3/Series/AniDB/{id}/Refresh?createSeriesEntry=true`)
and dummy files linked to its episodes by hand (`POST /api/v3/File/{id}/Link`). They correct the
table above where they differ.

- **Sign-in:** `POST /api/auth` with `{"user","pass","device"}` answers `{"apikey": "…"}`. A wrong
  password answers 401, and so does a request with no `apikey` header or a wrong one.
- **Episodes owned, in one request per series:**
  `GET /api/v3/Series/{id}/Episode?includeFiles=true&includeDataFrom=AniDB&type=Episode&pageSize=0`
  lists only the episodes that have files, since `includeMissing` defaults to off. Each comes with
  `AniDB.EpisodeNumber`, `AniDB.Type` and its `Files`: `ID`, `Size`, `Imported`, and `Locations[]`
  with `RelativePath` and `IsAccessible`.
  - Without `includeDataFrom=AniDB` there is no `AniDB` block, so no episode number.
  - The type filter takes `Episode` (28 regular episodes for Frieren). `Normal` is not a value
    and is silently ignored: 97 came back, credits and trailers included.
  - A file's own `SeriesIDs` cross-references name Shoko and AniDB episode *ids*, not numbers.
- **The incremental read:** `GET /api/v3/File?sortOrder=-ImportedAt&exclude=Unrecognized&include=XRefs`.
  - The criterion is `ImportedAt`; the file field is called `Imported`.
  - **An unknown sort key is ignored without an error:** `-Imported` and `-Bogus` both answered
    200 in ID order. A test must check the order, not the status code.
  - Unidentified files have no `Imported` and sort last; `exclude=Unrecognized` drops them.
  - `Imported` is set when a file is identified, a manual link included.
- **A series' MAL ids aren't only its cours:** Frieren (AniDB 17617, 28 episodes) lists MAL 52991
  (the series) and 56885 (a set of shorts). So "the offset is the sum of the episodes of the MAL ids
  listed before it" applies only when the entry's episode count differs from the series' count. An
  entry with the series' own count is a plain match at offset 0.
- **Episode counts:** `Sizes.Total.Episodes` (regular episodes), `Sizes.Local.Episodes` (those with
  files).

## Design

### 1. Settings

- **Shoko URL** (for example `http://localhost:8111`, Shoko's default port, or `http://shoko:8111`
  next to the app in Docker).
- **Shoko user and password, once**: the settings page sends them to Shoko's `POST /api/auth`
  (device `AutoAnimeDownloader`) and keeps **only the API key** Shoko returns; the password is never
  saved. The key is stored like a webhook token: never echoed back by the API or the CLI after
  saving, and redacted from bug reports. Signing in again replaces the key.
- **Use Shoko** on/off. Off: nothing below happens, as today.
- **Check Shoko files and look for upgrades**: **on by default**. Off: episodes in Shoko are only
  skipped and shown as **In Shoko**, with no quality verdict and no upgrade search (§4).
- **Download upgrades for episodes in Shoko**: **off by default** (the user's rule). Only matters
  while the check above is on.
- **Where Shoko upgrades go**: **a download folder in Shoko** (one inside Shoko's import folders,
  §7: the app asks Shoko to import it when it finishes) or **the app's library** (like any download
  of this app).
- **Delete the replaced Shoko file**: after an upgrade lands, the old Shoko file waits on the
  Replaced page for its waiting period, then the app asks Shoko to delete it
  (`DELETE /api/v3/File/{id}?removeFiles=true`). Shoko allows that only to an **admin** user: with
  a non-admin key the old file stays, and the Replaced page says to remove it in Shoko.

### 2. Matching an anime to a Shoko series

The app's anime are AniList entries; Shoko's series are AniDB anime. **The bridge is the MAL ID**:
every AniList entry has `idMal`, and every Shoko series lists its MAL IDs. No outside mapping file
is needed.

- **One AniList entry, one Shoko series** sharing a MAL ID, same episode count: a match; episode N
  is episode N.
- **A Shoko series listing several MAL IDs** (AniDB merged what MAL and AniList split into cours):
  the entry's episodes sit at an offset inside the Shoko series. The offset is the sum of the
  episodes of the MAL IDs listed before it — the same reasoning as the series axis
  ([#77](../../agents/decisions/077-the-per-series-absolute-axis-is-a-bfs-two.md)). When the counts
  don't add up, the match is shown as **partial — check by hand**: the anime page lets you confirm
  the Shoko series and the offset, and until then the app treats the anime as not in Shoko.
- **No MAL ID in common**: no match. A manual match on the anime page (pick the Shoko series)
  covers the rest.

Matches are cached per AniList id and refreshed when Shoko's series list changes.

### 3. Episodes that are already in Shoko

Before searching, every pending episode that has a file in its matched Shoko series is **owned in
Shoko**:

- the app does not search for it and does not download it;
- it is never deleted by the app's own rules: watched pruning, `delete_statuses`, the episode limit
  and the blacklist leave Shoko's files alone. The one delete the app asks Shoko for is a file an
  upgrade replaced (§1, "Delete the replaced Shoko file");
- the episode table shows it as **In Shoko**, with the file's name;
- a pack chosen for the other episodes still brings its file, as a torrent downloads whole. That
  copy is the app's, not Shoko's: with **Download Full Megapacks** on (on by default, for the whole
  list), the app records it like the rest of the pack, and its own rules apply to it
  (decisions/149). Off, or for an anime set to **Remove them** on its page, it is removed: skipped
  with qBittorrent (priority 0), deleted once downloaded otherwise, and a rain pack then stops
  seeding (decisions/153). In a download folder Shoko imports from (§7), Shoko imports what stays,
  and removing only skips: a file written there is Shoko's.

### 4. Is it good? — the same filters

Only while **Check Shoko files and look for upgrades** is on (§1). Off, this section does nothing:
no verdict, no upgrade look-ups, no Nyaa requests for episodes that are in Shoko.

Each Shoko file goes through the release filters
([release-filters](2026-11-23-release-filters-design.md) §4) as if it were a release, with better
data than a release name. `MediaInfo` and `_AniDB` give the real resolution, codec, bit depth,
audio and subtitle languages and bitrate; the release group and source come from AniDB. The file
gets the tier of the best filter it matches.

- **At the cutoff** (the best filter that applies to the anime): **In Shoko — match**. Nothing to
  do.
- **Below it**: **In Shoko — upgrade possible**. The episode joins the upgrade look-ups (the daily
  sweep and the search queue). A better release that turns up is listed on the Replaced page as an
  upgrade waiting, and **is not downloaded** unless "Download upgrades for episodes in Shoko" is on,
  or you approve that one.
- **No filters apply to the anime**: **In Shoko**, with no verdict.

### 5. Shoko groups

The anime page and the franchise group view
([megapack-downloads](2026-11-23-megapack-downloads-design.md) §2) show the **Shoko group** the
anime belongs to (from `IDs.TopLevelGroup`): its name and, per entry, In Shoko — match, In Shoko —
upgrade possible, partial match, or not in Shoko.

### 6. Syncing with Shoko

**Refresh Shoko** is a setting, in hours or days, **1 day by default**.

- **The first sync is full:** every series (`GET /api/v3/Series`, 100 per page), then the files of
  the series that match an anime the app tracks (`GET /api/v3/Series/{id}/File`). The app
  remembers the newest file it has seen: its import time and its file id.
- **Every refresh after that only looks for what is new:** `GET /api/v3/File` sorted by import
  time, newest first (`sortOrder=-ImportedAt`, up to 1,000 per page), read page by page until it
  reaches the file remembered last time. Each new file names the series it belongs to; a series
  the app hasn't seen yet is fetched by id (`GET /api/v3/Series/{id}`) and matched (§2). Only the
  anime those new files touch are re-judged (§4). A day of new files is usually one request.
- **What a "newer only" refresh can't see:** a file or series deleted in Shoko, a file moved to
  another episode, media info Shoko re-read. A **Full resync** button in the settings runs the
  first sync again.

**Live changes (built, `shoko_watch`, decisions/148):** the app listens to Shoko's SignalR hub, and
a file linked or deleted refreshes that series within seconds. Measured on v5.3.3:
- **The hub and feed:** the hub is `/signalr/aggregate`, and the file events (`ShokoEvent:FileMatched`,
  `FileDeleted`, both naming the series) are on the `shoko` feed.
- **No series yet:** `FileDetected` and `FileHashed` don't name a series.
- **No event:** unlinking a file from an episode sends none. A reconnection's catch-up reads every
  followed series again, which sees it; otherwise a Full resync does. The daily refresh stays
  newer-only, as decided.

### 7. Downloading into Shoko's import folders

Asked on 2026-11-24: "when downloads are finished, send a scan new files request, if the option
(to be created) downloading in shoko drop folder is on", and, for a pack, "wait for shoko to add
it and track on our side too". Then: the drop folder "should act as a strong suggestion rather
than option", Shoko's folders offered with the user's final say, and seeding not disabled. Last:
"you use the ones from shoko and send the signal to scan for new, not manage the drop folders for
shoko, only show this folder is in shoko or not". Direct mode only.

- **Shoko's folders, read, never managed.** The app reads Shoko's import folders
  (`GET /api/v3/ImportFolder`, with each sync, kept on disk) and no setting says which folder is
  Shoko's: a download folder is **in Shoko** when it is one of them or lies inside one, as Shoko
  sees it through `shoko_path_map` (the user's choice for Docker, where the two see a folder at
  different paths). Each download folder says In Shoko or Not in Shoko on the settings page and in
  `shoko status`. An earlier `shoko_drop_folder` setting, then a switch per download folder, were
  dropped for this.
- **Shoko's folders offered to add.** Download Folders lists Shoko's folders no download folder
  points at yet, the path translated through the map and editable; one click adds it (with an empty
  list the library stays a download folder after it). The user's Save is the final say.
- **Per folder**, since `feat/download-roots` (several download folders, the direct-library-mode
  spec's PR 2) met this branch: the user downloads some things outside Shoko (2026-11-24), so a
  folder outside Shoko behaves like any download folder (no import request, no hand-off, the app's
  own deletion rules), and an anime's own folder can send it outside Shoko. A torrent is handed off
  by the folder its placement is in.
- **Shoko never sees a half-written file.** Shoko imports only its `Import.VideoExtensions` (MKV,
  MP4…), so a `.part` name is invisible to it. A download folder in Shoko needs
  `incomplete_files: "part"` or an `incomplete_path` (staged files reach the library only
  finished); `"in_place"` without one is refused. With qBittorrent, only `"part"`: qBittorrent moves a staged torrent in itself, under
  the final names.
- **When a download finishes**, organize records the paths as usual, then:
  1. **Seeding goes on.** Shoko imports a file where it lies unless its renamer moves it
     (`Plugins.Renamer.MoveOnImport`, off on a fresh v5.3.3).
  2. **The records are marked handed to Shoko**, and the app keeps them: the episode counts as
     downloaded, and the anime page says **Waiting for Shoko** until Shoko has a file for it, then
     **In Shoko**.
  3. **The app asks Shoko to import new files** (`GET /api/v3/Action/ImportNewFiles`: 200 with the
     key, 401 without, measured on v5.3.3), so it doesn't wait for Shoko's folder watcher, which
     may be off (`WatchForNewFiles`). Completions close together share one request. A failed
     request is logged, shown in the Shoko group, and sent again with the next pass.
- **Shoko moved the files** (the user's "stop seeding those"): a handed-off torrent whose files are
  gone while Shoko has one of its episodes leaves the client, the rest kept, its records released.
  Without Shoko having any of them, a missing file is the user's deletion, as for any torrent.
- **A pack** brings every episode in it; what happens to the ones Shoko already had is §3's switch.
  A second file of an episode is Shoko's to keep or drop
  (its `Import.AutomaticallyDeleteDuplicatesOnImport`).
- **Deleting** a handed-off episode in the app removes its record, never its file: the file in
  a Shoko folder is Shoko's, and the app never deletes Shoko's files (§3). A torrent still
  seeding leaves the client with its files kept.

## What this does not do (for now)

- **Write to Shoko**, beyond signing in, asking it to import new files from its folders (§7)
  and deleting a file an upgrade replaced: no rename, no marking as watched.
- **Use Shoko for metadata.** AniList stays the source of truth; Shoko only says what you already
  have and how good it is.

## Delivery

1. **Read-only matching** — settings, the client, MAL-ID matching with offsets, "In Shoko" in the
   episode table, no download for owned episodes, never deleting them. Then the full megapacks
   switch (§3) and downloading into Shoko's own import folders (§7), on the same branch.
2. **Quality verdict** — Shoko files through the filters, match / upgrade possible, the Shoko group
   on the anime page.
3. **Upgrades** — owned episodes in the upgrade look-ups, waiting upgrades on the Replaced page, the
   download switch, the destination setting, deleting the replaced Shoko file.

## Decided (2026-11-23)

| # | Question | The user's answer |
|---|---|---|
| — | Upgrades for episodes in Shoko | **Looked for, not downloaded by default**; good files shown as **already in Shoko — match** |
| 1 | Signing in | **User and password once**; the app keeps only the key Shoko returns |
| 2 | MAL IDs line up, episode counts don't | **"Check by hand"** on the anime page; nothing automatic until confirmed |
| 3 | Where a Shoko upgrade goes | **A setting**: a Shoko drop folder or the app's library |
| 4 | The old Shoko file after an upgrade | **Deleted after the waiting period**, through Shoko's API (admin user needed) |
| 5 | Judging Shoko files and looking for upgrades | **A setting** to turn it on or off (proposed: on, since the request was to look for upgrades) |
| 6 | Refreshing Shoko's list | **A setting, 1 day by default**; after one full sync, only **newer files and series** are read |
| 7 | A pack that brings episodes already in Shoko (2026-11-24) | **Download Full Megapacks** for the whole list, on by default, and **per anime**: download them (the app records them) or remove them: **skip if not downloaded, otherwise delete** |
| 8 | Downloading for Shoko (2026-11-24) | **Shoko's own folders**: the app reads Shoko's import folders and never manages them; each download folder shows whether it is in Shoko, through a **Shoko path map**; Shoko's folders are offered to add, the user has the final say; when a download finishes in one, the app asks Shoko to **import new files** |
| 9 | Seeding once Shoko has the file (2026-11-24) | **Goes on**; a torrent whose files Shoko moved **stops seeding** (it leaves the client, files kept) |
| 10 | Several download folders, each with its own Shoko switch (2026-11-24) | Reading Shoko's import folders here; **several folders on their own branch** (the direct-library-mode spec's PR 2) |

## Proposed defaults — say if you disagree

Built on 2026-11-24: the warning on a file Shoko can't reach (`shoko_file_unavailable`), and the
automatic full resync, `shoko_full_resync_days`, 30 by default (the user: "add the automatic
sync").

- **Any file linked to the episode counts as In Shoko**, even when Shoko can't reach it right now
  (`Locations[].IsAccessible` false, an unplugged drive): downloading again would make a duplicate.
  The episode shows a warning instead.
- Where Shoko upgrades go starts as **the first download folder in Shoko** when there is one,
  else the app's library.
- A **full resync also runs every 30 days** on its own, so files deleted in Shoko stop counting as
  In Shoko without waiting for you to press the button.
- **Download Full Megapacks starts on**: in direct mode it costs no slot while
  `auto_delete_downloads` is off (the episode limit is lifted) and adds no file that isn't on disk
  anyway.
- **Deleting a handed-off episode** removes only the app's record; the file stays Shoko's (§7).
- **Removing a pack's episodes in Shoko needs the anime's episode total**: without it no file is
  mapped to an episode, and nothing is removed (decisions/153).
