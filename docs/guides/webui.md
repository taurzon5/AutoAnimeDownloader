# WebUI Guide

This guide covers the AutoAnimeDownloader web interface.

## Overview

The WebUI is a Svelte single-page app embedded directly in the daemon binary — no separate installation or server needed. It uses hash-based routing, so URLs look like `http://localhost:8091/#/status`.

## Accessing the WebUI

After starting the daemon, open **http://localhost:8091** in your browser.

## Pages

### Status (`/` or `/status`)

The main dashboard:

- **Daemon card** — current status (stopped/running/checking), last check time, next check countdown, and Start/Stop/Force-check controls
- **Library card** — total anime and episode counts
- **Disk card** — with several download folders (direct mode), one line per folder: its name and free space, red when it can't take a new torrent. Otherwise free/total space on the library's filesystem (`completed_anime_path`, where torrents also download), with a warning color when space is low
- **Recently downloaded** — a horizontal shelf of the animes with episodes downloaded in the last `recent_shelf_days` (3 by default, Downloads settings; 0 turns it off) that you haven't watched yet, newest first, at most 12. Each card shows the cover, a NEW badge, the title, the episodes ("Episode 5", "Episodes 1–3, 5", "1–12 · pack") and how long ago, and opens the anime. Watching the episodes takes the card off; opening it doesn't. With the user filter on, it follows the picked accounts' progress, like the table. Absent when there is nothing
- **Last check** — the problems and limits of the last check pass; each row opens its anime
- **Monitored animes** — searchable table of tracked anime (progress, last download, blacklist status). Type is a radio (Airing / Finished / Standalone / Blacklist, or All); Unwatched is an independent toggle so Finished+Unwatched is the binge queue. Counts follow search + Unwatched. With two or more list accounts there is also a **user** filter: a multi-select; an anime counts for an account when that account has it in one of the download statuses; several accounts are OR, and the progress, Unwatched and every count then follow the selected accounts' own progress instead of the slowest account's. Active filters persist in the querystring (`q`, `unwatched=1`, `type`, `account=<anilist|mal>:<user>`; an account no longer configured is dropped from it with a notice, and the username's case doesn't matter). When an account's list couldn't be fetched, picking it shows a notice saying so instead of an empty list; the X next to `N of M` clears them all. Click a row to open its detail page

Status changes push over WebSocket, so the page updates live without a refresh.

### Anime Detail (`/status/:id`)

Per-anime episode list, opened by clicking an anime on the Status page:

- **On lists** — every configured account that has the anime on its list, in any status, with its own status, progress (`ep 7/12`) and score in that account's own format (`85/100`, `8.5/10`, `★ 4/5`, a face for AniList's 3-point scale; MAL is always `x/10`), avatar and a link to the account's list
- Episode table (episode number, Anilist watch status, downloaded state)
- **Download queue** (once you have two queues or more): the queue the anime searches in and ranks
  with; changing it saves at once
- Header **Replace torrent** opens the [torrent picker](#torrent-picker) (`/status/:id/torrents`)
- An episode's **Choose torrent** (in its row menu) opens the same picker on that one episode's
  releases (`/status/:id/episodes/:n/torrents`), all of them, since a hand pick ignores the size and
  seeder limits; downloading one first asks, then replaces the episode if it was downloaded
- Header **Delete**: an anime you added by hand is removed from the app, its downloaded files with
  it unless you untick **Delete downloaded files**. An anime from a list can't be removed (the list
  would bring it back): the dialog names the account and offers **Delete downloaded files** (they
  won't download again) and **Blacklist it**, ticked by default. Blacklisting stops checking and
  downloading the anime and pauses its unfinished downloads; its files stay. A blacklisted anime
  shows **Blacklisted** here and on the Status page, with **Remove from blacklist** in the header
- Per-episode actions: download, delete, re-download, replace with a manual magnet link (the
  per-episode modal stays here)
- **Torrents** — below the episode table, one card per torrent that brought the anime's episodes:
  the release name (linked to its Nyaa page), the episodes it covers, its group and size, its state
  (downloading, seeding, in the library without seeding, not in the client, removed), how it came
  (a check, a manual download, the picker, a pasted magnet), when it was added and uploaded, the
  seeders when it was chosen, and chips for what its name says: resolution, source and service,
  codecs, audio and subtitle languages. A value the name doesn't state is assumed (Japanese audio,
  English subtitles, WEB-DL) and its chip is dashed with a "?". A torrent added before release
  details were kept says so. **Keep Cards of Removed Torrents** (Downloads settings, on by default)
  keeps the card of a torrent that is gone, marked removed
- Bulk actions on selected episodes: download, delete, release (unpin from retry queue)
- File list of the torrent attached to an episode, when metadata is in

### Add anime (`/add`)

Search AniList and add a title as a **standalone** anime (tracked without being on any AniList
list). An AniList account is optional; this is the entry point for an install with none.

With more than one [download queue](#priorities-priorities), a **Queue** choice sits under the search: the
anime goes into that queue before its first search, here or from the picker.

Clicking Add on a card first searches ranked whole-title torrents. An empty first search adds
without opening the picker (singles, or track-with-zero for an unreleased title). A non-empty
list, or Nyaa being down, opens the [torrent picker](#torrent-picker).

A blacklisted anime says **Blacklisted** on its card. Adding it asks first: confirming takes it off
the blacklist and adds it.

<a id="torrent-picker"></a>

### Torrent picker (`/add/:id/torrents`, `/status/:id/torrents`)

Confirm or swap the ranked whole-title torrent before anything is saved. The header is the
anime (cover, title, format · episodes) plus a packs-only count line — this screen lists
packs, not singles. At most 8 rows; the first is the auto-pick. Selecting a row loads its Nyaa
description on demand (the other rows are not fetched). The query field is a labeled Nyaa `q`
with a Search button and is not saved.

A row whose name says it holds more than one season or the movies (`S01-05`, `Complete Series`,
`+ Movies`) carries a **Megapack** chip. When the selected torrent's videos belong to two entries
or more of the anime's franchise, the detail shows **What goes where**: each entry (season, movie,
OVA) with its format, year, the episodes found (`Episodes 1–26 of 26`) and their size, this anime
marked, and a fold with the files no entry takes and why. The table only shows what is inside: the
whole torrent still downloads.

**Download this** sits in the list card and always posts the highlighted row. The magnet strip
below the grid has **Download magnet** for a pasted URI; a paste does not override the list
button. Add returns to Add; Replace returns to the anime page. Cancel saves nothing. From the
anime page (Replace torrent), both buttons first ask: every downloaded episode of the anime is
deleted with its files (files handed to Shoko stay), then the torrent downloads in their place. The per-episode replace modal
stays on Anime Detail.

### Downloads (`/downloads`)

Live torrent table for the embedded client: progress, speeds, queue order, pause / resume /
announce / prioritize / delete (single or bulk). Expanding a row shows the files inside the
torrent.

### Config (`/config`)

Daemon configuration, grouped into sections: Library, Lists (AniList and MyAnimeList usernames, Excluded Lists, and **Download Custom Lists**: an AniList custom list whose anime all download, whatever their status on AniList), Downloads, Torrent Client, Shoko, General (Check for Updates, First Steps, Keep Cards of Removed Torrents, the Recently Downloaded Shelf) and Torrent Search (advanced). Covers paths, check interval, episode limits/retries, and download/delete rules. Deleting watched episodes and by list status sits in Library under **Delete Downloads Automatically**: Delete Watched Episodes Automatically, Watched Episodes to Keep and List Statuses to Delete (the per-anime episode limit, which also deletes, stays in Downloads). While direct mode keeps downloads forever they are dimmed with a line saying they only reach downloads added in hardlink mode. The **Library Folder** is the only required setting: in direct mode, the default, each torrent is written straight into the library (series and season folders) and seeds from there; with **Write Downloads Straight to the Library** off (hardlink mode), torrents download and seed in `<path>/.torrents` and completed episodes are hardlinked into the library, and saving a path whose filesystem doesn't support hardlinks (exFAT/FAT32, some SMB shares) is rejected. The **Torrent Client** group switches from the embedded client to your own qBittorrent 5.0+ (`torrent_client`): its WebUI address, login or API key, category and an optional folder mapping, with a **Test connection** button (`POST /torrent-client/test`) that tries the values before they are saved: it says whether the API key or the password signed in, checks that qBittorrent sees the library (and the incomplete folder, when set, and each download folder), and shows qBittorrent's own download queue. **Let the app manage this qBittorrent's settings** (off by default) sets qBittorrent's download queue from Max Concurrent Downloads, turns off its ".!qB" extension and moves memory-mapped disk I/O to "Simple pread/pwrite" (after a qBittorrent restart); leave it off for a qBittorrent you also use for other things. In direct mode, **Download Folders** spreads torrents over several folders: a new torrent goes to its anime's folder (picked on its page, **Download folder**), else the folder its series already lives in, else the first folder in the list with room: each folder fills until its drive would keep less than its **Keep Free (GiB)** (empty = Min Free Disk Space), then the next one takes over. Move a folder up to make it the highest priority. Once the library is one of the folders, its row is marked **Library** and carries its path: the Library Folder field is replaced by a line naming that folder, and changing the row's path moves the library. When the low-disk alert has several folders, it speaks only when even the roomiest one runs low, and names it. The order only decides where new torrents go: what is already downloaded stays in its folder, and a new episode of a series still goes next to the series. Each folder must already exist. The incomplete download folder works with qBittorrent too: it downloads there and moves the episode into the library itself. With qBittorrent, **Disable Seeding** means almost no upload: each torrent is held to an upload limit (16 KiB/s by default, set under it in Downloads) until its episode is in the library. The **Shoko** group starts with the sign-in; **Use Shoko** below it stays greyed out until you are signed in, and the rows that depend on it are dimmed while it is off. It signs in to a Shoko Server: the password is sent once, and only the key Shoko returns is kept. It then turns on **Use Shoko**: episodes already in your Shoko library are never searched for or downloaded, the anime page marks them **In Shoko** with the file's path (with a warning when Shoko can't reach the file right now; their Download button moves to the ⋯ menu), and a **Shoko** panel there says which Shoko series the anime matched. A partial match, where the series shares the MAL id but the episodes don't line up, is confirmed by hand with the series id and offset (the Shoko episode number of this anime's episode 1, minus 1); a clean match keeps those fields behind **Correct the match**, and **Not in Shoko** overrides a wrong match. The group also sets how often Shoko is read again, has a **Full resync** button (and **Read All of Shoko Again Every**, 30 days by default, for the one change Shoko doesn't announce: a file unlinked from an episode), and **Follow Shoko's Changes as They Happen** (on by default): a file added to an episode or deleted in Shoko shows up within seconds, and the group says whether it is connected. A pack chosen for your missing episodes downloads whole, so it also brings the episodes you have in Shoko: **Download Full Megapacks** (on by default, for the whole list) records them as the app's own, so they show as downloaded and follow the usual rules; off, they are removed: never downloaded with qBittorrent, deleted once the pack finishes otherwise (the built-in client then stops seeding that pack). The anime page's Shoko panel chooses for one anime (Default, Download them, Remove them). The group also lists **Shoko's import folders** as Shoko reads them; the app never manages them. When Shoko or this app runs in Docker, the **Shoko Path Map** says where this app sees each folder. In direct mode, each download folder then says **In Shoko** (with Shoko's folder) or **Not in Shoko**, checked when you save, and Download Folders offers Shoko's folders this app doesn't download into yet: one click adds it, path translated and editable (with an empty list the Library Folder stays a download folder after it); Save keeps it. Other folders take what you download outside Shoko. When a download finishes in a folder in Shoko, the app asks Shoko to import it at once and the torrent keeps seeding; a torrent whose files Shoko moved leaves the client. On the anime page such an episode says **Waiting for Shoko** until Shoko has it, then **In Shoko**; deleting it there removes only the app's record, never the file. qBittorrent needs direct mode, and the client is picked when the app starts: saving a change says to restart, and until the restart a line under the switch and a banner on Status say the app still runs the old client. The password and key are never shown again once saved. See [Config Reference](../agents/config.md) for the full field list and defaults.

If required config is missing, the daemon opens this page automatically with a `?missingConfig=true` banner.

### Blacklist (`/blacklist`)

Its own entry in the sidebar (in **More** on a phone). Lists the anime on the app's blacklist,
newest first, with when they were blacklisted, how many downloaded episodes the app still has and
how many downloads the blacklist paused. **Remove from blacklist** takes one off: the app checks and
downloads it again from the next check and resumes the downloads the blacklist paused. Anime in an
AniList list named in Excluded Lists are not listed here; that list is changed on AniList.

### Priorities (`/priorities`)

Reorderable lists that drive torrent ranking during search (fansub groups, resolutions, sources, codecs, audio, an ignore list, and the order criteria are applied in). Sources rank movies and series whose AniList media status is FINISHED; audio stays movies-only. Restore-defaults is how an existing install picks up the new `criteria_order`. See [Config Reference](../agents/config.md#fields) for what each list does.

**Download queues** sit at the top. The app starts with one, Default, which holds every anime you
haven't put in another queue (those from your AniList and MyAnimeList lists included). Add a queue
by name: it starts with a copy of the default ranking. The order is the search order: each check
searches every anime of the first queue, then the second, and so on, so a later queue waits for
the earlier ones. Downloads don't wait: they share Max Concurrent Downloads and start as slots free
up, whatever their queue. Rename, move up or down, or delete a queue (its anime go back to
Default; Default can't be deleted). With two queues or more, **Ranking of queue** picks which
queue's lists the page edits; Save saves every queue you changed. The ignore list is shared by all
queues, so it shows only with Default selected. An anime is put in a queue on the Add screen or on
its page (**Download queue**).

### Notifications (`/notifications`)

Webhook configuration: add/edit/remove webhook presets (name, URL, method, headers, body template), pick which events trigger each one, grouped as Episodes (new episode, download failed, download completed, episode deleted), Library (an anime hit the episode limit, an episode has not downloaded for two days, a finished anime is all in the library, the next episode has waited 30 days to be watched, a weekly digest of watched files still on disk) and System (the library folder vanished, the daemon crashed, checks keep failing, a new version is out, the disk is almost full or full), narrow it to **which animes** (the animes on some accounts' lists, as the last check read them, and/or the standalone ones — nothing checked means every anime; an anime none of the lists holds still goes to every filtered webhook), and send a test request (the test ignores the anime filter). Presets include the usual notification services plus **Jellyfin** and **Plex** — those two fire a library scan on `download_completed` and `episode_deleted` instead of a message (fill in the URL, the token and, for Plex, the library section id). Template variables like `{{title}}`/`{{message}}` are documented in [Config Reference](../agents/config.md#webhook-template-variables).

### Logs (`/logs`)

Real-time daemon logs over WebSocket:

- Line count and level filter (all/debug/info/warn/error), text search
- Autoscroll and live-tail toggles, copy-to-clipboard
- Manual reload button

### Report (`/report`)

Builds a redacted diagnostics bundle (environment, config, log, optional crash file) to attach to
a GitHub issue. Consent is the act of attaching the file; there is no backend. The tray menu and
the crash banner on Status also land here. See [decisions/102](../agents/decisions/102-the-bug-report-has-no-backend-consent-is-the-act.md).

## Troubleshooting

**Blank page / WebUI not loading**
- Confirm the daemon is running and reachable at `http://localhost:8091/api/v1/status`
- Check the browser console for errors
- Hard refresh (`Ctrl/Cmd + Shift + R`) if styling looks broken — usually a stale cached bundle

**Status/logs not updating live**
- Check the WebSocket connection isn't blocked by a firewall or proxy
- Refresh the page to reconnect

## See Also

- [CLI Guide](cli.md) — command-line equivalent for most of these actions
- [Config Reference](../agents/config.md) — full config field reference
