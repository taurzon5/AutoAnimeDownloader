# Shoko integration: what you already have counts, and only its upgrades are looked for

**Date:** 2026-11-23
**Status:** designed, not implemented — read from Shoko Server's source, not yet tried against a
running server; the user's decisions are recorded at the end
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
- **Where Shoko upgrades go**: **a Shoko drop folder** (a folder Shoko watches as a drop source;
  Shoko hashes, identifies, renames and moves the file into its library) or **the app's library**
  (like any download of this app).
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
- the episode table shows it as **In Shoko**, with the file's name.

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

Shoko's SignalR events (file matched, file deleted) could replace the polling later.

## What this does not do (for now)

- **Write to Shoko**, beyond signing in and deleting a file an upgrade replaced: no import calls,
  no rename, no marking as watched. A file put in a drop folder is imported by Shoko on its own.
- **Use Shoko for metadata.** AniList stays the source of truth; Shoko only says what you already
  have and how good it is.

## Delivery

1. **Read-only matching** — settings, the client, MAL-ID matching with offsets, "In Shoko" in the
   episode table, no download for owned episodes, never deleting them.
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

## Proposed defaults — say if you disagree

- **Any file linked to the episode counts as In Shoko**, even when Shoko can't reach it right now
  (`Locations[].IsAccessible` false, an unplugged drive): downloading again would make a duplicate.
  The episode shows a warning instead.
- Where Shoko upgrades go starts as **the Shoko drop folder** when one is set, else the app's
  library.
- A **full resync also runs every 30 days** on its own, so files deleted in Shoko stop counting as
  In Shoko without waiting for you to press the button.
