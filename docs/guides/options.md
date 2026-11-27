# Options Guide

Every option in AutoAnimeDownloader, each with an example of when you would change it. Options
are named the way the web UI labels them, followed by their `config.json` key and the CLI line
that sets them.

The guide has two parts:

- [In the app today](#in-the-app-today): what you can set now.
- [Planned (designed, not built)](#planned-designed-not-built): designed but not in the app yet.
  Don't look for these settings: they don't exist.

This guide is for using the app. For types, validation rules and the reasons behind them, see the
[Config Reference](../agents/config.md).

## How to change an option

All three ways write the same file:

- **Web UI.** The Config page (`#/config`), the Priorities page and the Notifications page. Each
  has its own Save button.
- **CLI.** `autoanimedownloader config set <key> <value>`; `config get` prints every key. A list
  takes comma-separated values (`a,b`) or JSON (`'["a","b"]'`); a list of objects takes JSON. A
  dotted key changes one field inside an object and keeps the rest:
  `config set priorities.fansubs subsplease,erai-raws`. Full syntax: [CLI Guide](cli.md).
- **By hand.** `config.json` lives in the data folder: `~/.autoAnimeDownloader/` on Linux and
  macOS, `%APPDATA%\.autoAnimeDownloader\` on Windows, `/app/data/.autoAnimeDownloader/` in
  Docker. Nothing checks a hand edit, so prefer the two above.

A key missing from `config.json` takes its default.

### Two things to know first

**Downloads are kept forever by default.** A new install writes downloads straight into the
library ([direct mode](#write-downloads-straight-to-the-library)) with
[Delete Downloads Automatically](#delete-downloads-automatically) off. In that setup watched or
dropped episodes are never deleted: the watched-episode cleanup, List Statuses to Delete and the
episode limit are dimmed and don't touch new downloads. Turn on Delete Downloads Automatically if
you want watched episodes cleaned up.

**Two torrent clients.** The built-in client is the default. You can switch to your own
qBittorrent 5.0 or later ([Torrent Client](#torrent-client)). A few options work differently with
qBittorrent; each one says so.

## In the app today

The Config page groups its options like this part does: Library, Lists, Downloads, Torrent Client,
Shoko, General and Torrent Search (advanced). The Priorities page, the Notifications page, the
anime page, Docker and environment variables follow.

### Library

#### Library Folder

`completed_anime_path` · default `~/Animes` · the only required setting

Your media-server library. In direct mode each torrent is written straight into it, one folder per
series. With [Download Folders](#download-folders) set, new torrents go to the library only if it
is in that list, and its path is then edited there.

Example: your library is on a second drive.

```bash
autoanimedownloader config set completed_anime_path /mnt/media/Anime
```

#### Write Downloads Straight to the Library

`library_mode` · `direct` (default) or `hardlink`

**On (`direct`)**: each torrent is written once, in its final place, and seeds from there. No
second copy and no hardlinks, so it also works on drives without hardlink support (exFAT, some
network shares).

| Torrent | Goes to |
|---|---|
| One episode | `<Library>/<Series>/Season 02/<file>` |
| A movie | `<Library>/<Series>/Specials/<file>` |
| A pack | `<Library>/<Series>/<the torrent's own folders>` |

`<Series>` is the first season's title, so every season of a show lands in one folder.

**Off (`hardlink`)**: torrents download and seed in `<Library>/.torrents`, and finished episodes
are hardlinked into `<Library>/<Anime Name>/`, one folder per AniList entry. This mode is kept for
installs that already use it. A new install should leave direct mode on, and qBittorrent needs it.

Changing it only affects torrents added afterwards: what is already downloaded stays where it is.

Example: an older install that still uses hardlinks wants new downloads written in place.

```bash
autoanimedownloader config set library_mode direct
```

A file you delete by hand (from Jellyfin, a file manager) is not downloaded again on its own: its
row on the Downloads page says **File not found**. **Redownload missing files**, above that list,
fetches it again into the same place.

```bash
autoanimedownloader torrents redownload-missing
```

#### Rename Files to a Standard Format (Useful for Plex/Jellyfin)

`rename_files_for_jellyfin` · default off

In direct mode, names each downloaded episode `Series - S02E05.mkv` inside its series and season
folders; packs and movies keep the names they came with. In hardlink mode, names the library link
`Anime Name - E05.mkv`, the files inside packs included.

Example: Jellyfin keeps matching episodes to the wrong numbers.

```bash
autoanimedownloader config set rename_files_for_jellyfin true
```

#### Number Split Seasons Continuously

`episode_numbering` · `season` (default, the switch on) or `entry`

Shown in direct mode with renaming on. Some seasons come as two AniList entries (Part 1 and
Part 2), each numbered from 1. On, Part 2's first episode becomes `S02E13`, the way TVDB and TMDB
number it. Off keeps AniList's numbers and adds a part tag: `S02E01 (Part 2)`.

Example: your media server uses AniList's numbering, not TVDB's.

```bash
autoanimedownloader config set episode_numbering entry
```

#### Delete Downloads Automatically

`auto_delete_downloads` · default off

For torrents added in direct mode, even after you switch back to hardlink mode. **Off** keeps them
forever and keeps seeding them: watched or dropped episodes are never deleted, and the
[episode limit](#limit-episodes-per-anime) is ignored. **On** applies the three settings below and
the episode limit, and deleting a file also stops seeding it. Deleting by hand works either way.

Example: your disk is small and you want watched episodes gone.

```bash
autoanimedownloader config set auto_delete_downloads true
```

#### Delete Watched Episodes Automatically

`delete_watched_episodes` · default on

Deletes the episodes you've watched, keeping the last few (next option), and the episodes of
anime that left your lists. Dimmed, and without effect on new downloads, while downloads are kept
forever.

Example: you rewatch shows and want to keep everything.

```bash
autoanimedownloader config set delete_watched_episodes false
```

#### Watched Episodes to Keep

`watched_episodes_to_keep` · default `2`

How many of your last watched episodes stay, in case you marked one by mistake or want to rewatch.
`0` deletes every watched episode.

Example: keep the last five.

```bash
autoanimedownloader config set watched_episodes_to_keep 5
```

#### List Statuses to Delete

`delete_statuses` · default none · `CURRENT`, `REPEATING`, `PLANNING`, `PAUSED`, `DROPPED`,
`COMPLETED`

An anime moved to one of these list statuses has its downloaded episodes deleted. With several
accounts, **every** account that has the anime must have it in one of these statuses (not
necessarily the same one). This is the opposite of downloading, where one account is enough.

Example: dropping a show should free its space.

```bash
autoanimedownloader config set delete_statuses DROPPED
```

#### Hide Unfinished Files

`incomplete_files` · `part` (default, the switch on) or `in_place`

Direct mode only. On, a file is written as `name.part` while it downloads and gets its real name
when the torrent finishes, so the media server never picks up a half-downloaded episode. Off
writes the real name from the start. No effect while an
[Incomplete Downloads Folder](#incomplete-downloads-folder) is set.

Example: another tool watches the folder and needs the final names from the start.

```bash
autoanimedownloader config set incomplete_files in_place
```

#### Incomplete Downloads Folder

`incomplete_path` · default empty (off)

Direct mode only. Torrents download here and move into the library when they finish, so the
library disk takes one sequential write per file. Use a folder on an SSD or NVMe drive, especially
with a USB or SMR hard disk library. The folder must already exist, sit inside the drive (not its
mount point) and be outside the library. With qBittorrent it is qBittorrent's download folder, and
qBittorrent moves the finished files itself.

Example: the library is on a slow USB disk and you have an NVMe drive.

```bash
autoanimedownloader config set incomplete_path /mnt/nvme/aad-incomplete
```

#### Min Free Space for Incomplete Folder

`incomplete_min_free_disk_gb` · default `10` (GiB)

Like [Min Free Disk Space](#min-free-disk-space), for the incomplete folder's drive. Only checked
when that folder is on another drive than the library. `0` turns it off.

Example: the NVMe drive also holds your system and must keep 50 GiB free.

```bash
autoanimedownloader config set incomplete_min_free_disk_gb 50
```

#### Download Folders

`download_roots` · default empty (the Library Folder alone)

Direct mode only. Several folders to download into, each with a **Name**, a **Folder** and
**Keep Free (GiB)**. A new torrent goes to the anime's own folder (picked on its page,
[Download folder](#download-folder)), else the folder its series already lives in, else the first
folder in the list with room. Each folder fills until its drive would keep less than its Keep Free
(empty = Min Free Disk Space), then the next one takes over. The order only decides where new
torrents go: nothing already downloaded moves. Each folder must already exist.

Example: fill the SSD first, keeping 200 GiB free on it, then the hard disk.

```bash
autoanimedownloader config set download_roots \
  '[{"name":"ssd","path":"/mnt/ssd/Anime","min_free_gb":200},{"name":"hdd","path":"/mnt/hdd/Anime"}]'
```

`config set download_roots` replaces the whole list, so name every folder, the library included if
it should stay one. With [Shoko](#shoko) on, each folder also says whether it is in Shoko.

#### Min Free Disk Space

`min_free_disk_gb` · default `10` (GiB)

Space kept free on the library drive, after what queued downloads still have to write. A new
torrent that would go below it is refused. `0` turns the floor off, but a torrent bigger than the
free space is still refused. It is also the Keep Free of every download folder that doesn't set
its own, and the disk alert warns at twice this value.

Example: the drive is shared with other media and must keep 100 GiB.

```bash
autoanimedownloader config set min_free_disk_gb 100
```

### Lists

#### AniList Usernames

`anilist_usernames` · default none

The AniList accounts whose lists the app follows. Optional: you can also add anime one by one from
[Add anime](#adding-an-anime-by-hand). Several accounts work, for a household.

Example: you and your partner each have an account.

```bash
autoanimedownloader config set anilist_usernames me,partner
```

#### MyAnimeList Usernames

`mal_usernames` · default none

MyAnimeList accounts to follow. The list must be public. The app reads only which anime, their
status and your progress; everything else still comes from AniList. Excluded Lists and Download
Custom Lists don't apply: MyAnimeList has no custom lists.

Example: your list is on MyAnimeList.

```bash
autoanimedownloader config set mal_usernames mymaluser
```

#### Excluded Lists

`excluded_lists` · default none

AniList custom lists whose anime never download, whatever their status. It wins over everything
below. It is not the app's own [blacklist](#blacklist), which you manage in the app.

Example: you keep a custom list "Watching on Netflix" for shows you stream elsewhere.

```bash
autoanimedownloader config set excluded_lists "Watching on Netflix"
```

#### Download Custom Lists

`download_custom_lists` · default none

Every anime in these AniList custom lists downloads, whatever its status on AniList: Planning,
Paused, Completed and Dropped too. Each entry is an account and a list name. An anime in an
Excluded List still stays out, and being in one of these lists keeps it safe from List Statuses
to Delete. Airing Statuses to Download still applies. AniList only.

Example: you collect finished shows in a custom list "To download" and keep them as Planning.

```bash
autoanimedownloader config set download_custom_lists \
  '[{"account":"anilist:me","custom_list":"To download"}]'
```

#### List Statuses to Download

`download_statuses` · default `CURRENT`, `REPEATING` (Watching, Re-watching)

List statuses (AniList and MyAnimeList) whose anime download. The others are `PLANNING`, `PAUSED`,
`DROPPED` and `COMPLETED`. With several accounts, one account having the anime in one of these
statuses is enough.

Example: start downloading what you plan to watch.

```bash
autoanimedownloader config set download_statuses CURRENT,REPEATING,PLANNING
```

#### Airing Statuses to Download

`download_media_statuses` · default `RELEASING`, `FINISHED` (Releasing, Finished)

The anime's own airing state, checked on top of the list status. The others are `CANCELLED` and
`HIATUS`. An empty list downloads nothing. An anime that already has a downloaded episode is
never hidden by this filter.

Example: you also want shows that were cancelled or are on hiatus.

```bash
autoanimedownloader config set download_media_statuses RELEASING,FINISHED,CANCELLED,HIATUS
```

### Downloads

#### Check Interval

`check_interval` · default `10` (minutes)

Minutes between checks. Each check reads your lists and searches Nyaa for what's missing. **Check
now** on the Config page, or the CLI's `check`, runs one at once.

Example: check every half hour to make fewer requests.

```bash
autoanimedownloader config set check_interval 30
```

#### Max Concurrent Downloads

`max_concurrent_downloads` · default `3`

How many unfinished torrents download at the same time; the others wait their turn (shown as
queued on the Downloads page). Seeding is never limited. `0` means no limit. With qBittorrent it
applies only when
[the app manages qBittorrent's settings](#let-the-app-manage-this-qbittorrents-settings);
otherwise qBittorrent's own queue does.

Example: a slow connection, one torrent at a time.

```bash
autoanimedownloader config set max_concurrent_downloads 1
```

#### Disable Seeding (No Upload)

`disable_seeding` · default off

With the built-in client: never uploads to other peers, not even while downloading, and removes
each torrent once its episodes are in the library (in direct mode the files stay: they are the
library). Downloads may be slower, since peers favor those who upload back. With qBittorrent it
means almost no upload: each torrent is held to the
[upload limit](#upload-limit-with-qbittorrent) until its episode is in the library, then removed.

Example: your network must not upload.

```bash
autoanimedownloader config set disable_seeding true
```

#### Upload Limit with qBittorrent

`qbittorrent_disable_seeding_upload_kib` · default `16` (KiB/s)

Shown with qBittorrent and Disable Seeding on. qBittorrent can't refuse uploads, so this is the
per-torrent upload limit while it downloads. The download's own requests go through it too: a
download runs at most a few hundred times this (16 KiB/s allows roughly 5 to 16 MB/s). At least
`1`.

Example: you can afford a little more upload for faster downloads.

```bash
autoanimedownloader config set qbittorrent_disable_seeding_upload_kib 64
```

#### Limit Episodes per Anime

`max_episodes_per_anime` · default `0` (the switch off)

The switch, with **Max Episodes per Anime** under it. On, the app keeps at most that many
episodes of one anime at the same time, and a pack search covers only that many episodes ahead.
Off (`0`), every aired episode downloads, and a pack search covers all of them at once; the brakes
left are [Max Batch Torrent Size](#max-batch-torrent-size) and
[Min Free Disk Space](#min-free-disk-space). It never splits a pack: a pack is one torrent, so it
downloads whole. Greyed out while downloads are kept forever (direct mode with Delete Downloads
Automatically off). The **Anime hit the episode limit** notification says when one is held back.

Example: a small disk, at most 6 episodes of each show (with Delete Downloads Automatically on).

```bash
autoanimedownloader config set max_episodes_per_anime 6
```

### Torrent Client

#### Use qBittorrent Instead of the Built-in Client

`torrent_client` · `builtin` (default) or `qbittorrent`

Drives your own qBittorrent 5.0 or later through its WebUI instead of the client built into the
app. It writes straight into the library, so it needs
[Write Downloads Straight to the Library](#write-downloads-straight-to-the-library) on. The client
is picked when the app starts: restart it after changing this. Until then, the Status page and
`autoanimedownloader status` say a restart is needed.

Example: you already run qBittorrent and want the app's torrents there.

```bash
autoanimedownloader config set torrent_client qbittorrent
autoanimedownloader stop && autoanimedownloader start
```

#### qBittorrent WebUI Address

`qbittorrent_url` · default empty

Where qBittorrent's WebUI answers. A base path behind a reverse proxy is kept. With qBittorrent in
Docker, publish its WebUI on the same port inside and outside the container (`-p 8080:8080`):
qBittorrent answers a port mismatch with 401, which looks like a wrong password.

Example: qBittorrent runs on the same machine, on its default port.

```bash
autoanimedownloader config set qbittorrent_url http://127.0.0.1:8080
```

#### qBittorrent Username and qBittorrent Password

`qbittorrent_username`, `qbittorrent_password` · default empty

The WebUI login, used when there is no API key. The password is kept by the app and never shown
again: `config get` shows `__saved__` in its place, and saving the rest of the settings keeps it.
Type it again when you change the address. Prefer the web UI field for the password: `config set`
prints the value it saved back to the terminal.

Example: the WebUI user is `admin`.

```bash
autoanimedownloader config set qbittorrent_username admin
autoanimedownloader config set qbittorrent_password "$QBIT_PASSWORD"
```

#### qBittorrent API Key

`qbittorrent_api_key` · default empty

Optional, used instead of the username and password. It needs Web API 2.14.1 or later; you make
the key in qBittorrent's WebUI settings. Kept secret like the password.

Example: you'd rather not store a password. Make a key in qBittorrent and paste it in this field,
or:

```bash
autoanimedownloader config set qbittorrent_api_key "$QBIT_API_KEY"
```

#### qBittorrent Category

`qbittorrent_category` · default `autoanimedownloader`

Every torrent of the app gets this category. The app only touches torrents in it, so a qBittorrent
you also use for other things is left alone.

Example: your other tools already sort by category and you want `anime`.

```bash
autoanimedownloader config set qbittorrent_category anime
```

#### Let the App Manage This qBittorrent's Settings

`qbittorrent_manage_settings` · default off

On, the app sets qBittorrent's download queue from
[Max Concurrent Downloads](#max-concurrent-downloads) with no cap on seeding, turns off its
`.!qB` extension, and switches its disk I/O to "Simple pread/pwrite" (after a qBittorrent
restart). Leave it off for a qBittorrent you also use for other things: the app then changes
nothing global in it.

Example: this qBittorrent exists only for the app.

```bash
autoanimedownloader config set qbittorrent_manage_settings true
```

#### Folder Mapping

`qbittorrent_path_map` · default empty (same paths)

Only when qBittorrent sees the library at another path than the app, for example in Docker or on
another machine: each folder as the app sees it and as qBittorrent sees it.

Example: the app sees `/mnt/anime`, qBittorrent's container sees `/downloads`.

```bash
autoanimedownloader config set qbittorrent_path_map '[{"app":"/mnt/anime","qbittorrent":"/downloads"}]'
```

**Test connection** tries the values before you save them: how it signed in, whether qBittorrent
sees the library (and the incomplete folder and each download folder), and its own download queue.
The CLI tests the saved values:

```bash
autoanimedownloader torrents test
```

### Shoko

Shoko is optional. With it, episodes already in your Shoko library are never searched for or
downloaded, and the anime page marks them **In Shoko**. The app never deletes Shoko's files.

#### Signing in

`shoko_url`, `shoko_api_key` · set by the sign-in

Fill in **Shoko Address**, **Shoko Username** and **Shoko Password**, then **Sign in to Shoko**.
The password is sent once; the app keeps only the key Shoko returns, never the password.
**Sign out**, then Save, clears both. Don't set these two keys by hand.

Example: Shoko Server runs at `http://127.0.0.1:8111`.

```bash
echo -n "$SHOKO_PASSWORD" | autoanimedownloader shoko login --url http://127.0.0.1:8111 --user admin
```

#### Use Shoko

`shoko_enabled` · default off

Turns the integration on. It can only be on once you are signed in.

Example: you already have most of your collection in Shoko.

```bash
autoanimedownloader config set shoko_enabled true
```

#### Refresh Shoko Every

`shoko_refresh_hours` · default `24` (hours)

How often the app reads what is new in Shoko. The first sync reads everything; later ones only
what Shoko imported since. At least `1`.

Example: you import into Shoko several times a day and don't use the live follow.

```bash
autoanimedownloader config set shoko_refresh_hours 6
```

#### Follow Shoko's Changes as They Happen

`shoko_watch` · default on

Listens to Shoko: a file added to an episode or deleted there shows up within seconds instead of
at the next refresh. A deleted episode is downloaded at the next check. The one change Shoko
doesn't announce is a file unlinked from an episode: the next full resync catches it.

Example: Shoko is on another network and you'd rather not keep a connection open to it.

```bash
autoanimedownloader config set shoko_watch false
```

#### Read All of Shoko Again Every

`shoko_full_resync_days` · default `30` (days)

A full resync on its own, so a file unlinked from an episode stops counting. `0` = only when you
press **Full resync**.

Example: you reorganize Shoko often; resync weekly.

```bash
autoanimedownloader config set shoko_full_resync_days 7
autoanimedownloader shoko sync --full   # the Full resync button
```

#### Download Full Megapacks

`shoko_full_packs` · default on

A pack picked for your missing episodes downloads whole, so it also brings episodes you already
have in Shoko. On, the app keeps all of them as its own downloads. Off, it removes those: with
qBittorrent they are never downloaded; with the built-in client they are deleted once the pack
finishes, and the pack stops seeding. Each anime can choose for itself on its page
([Episodes in Shoko that a pack brings](#episodes-in-shoko-that-a-pack-brings)).

Example: you don't want two copies of episodes Shoko already has.

```bash
autoanimedownloader config set shoko_full_packs false
```

#### Shoko's Import Folders and Shoko Path Map

`shoko_path_map` · default empty (same paths)

The group lists Shoko's import folders as Shoko reads them. To download into Shoko (direct mode),
add one of them as a [download folder](#download-folders): when a download there finishes, the
app asks Shoko to import it at once, and the torrent keeps seeding. The app never deletes a file
it handed to Shoko. When Shoko or the app runs in Docker or on another machine, the Path Map lists
each folder as the app sees it and as Shoko sees it.

Example: the app sees `/mnt/shoko-drop`, Shoko sees `/import`.

```bash
autoanimedownloader config set shoko_path_map '[{"app":"/mnt/shoko-drop","shoko":"/import"}]'
autoanimedownloader shoko status   # Shoko's folders, and whether each download folder is in Shoko
```

### General

#### Check for Updates

`check_for_updates` · default on

Asks GitHub once a day whether a newer version is out, to show it next to the version and send
the **New version available** notification. Off, no request ever leaves.

Example: Docker or your package manager already tells you about updates.

```bash
autoanimedownloader config set check_for_updates false
```

#### Keep Cards of Removed Torrents

`keep_removed_release_cards` · default on

The anime page lists the torrents that brought its episodes. On, a torrent that is gone (watched
and deleted, removed by hand) keeps its card, marked removed. Off hides those cards; turning it
back on brings them back.

Example: you only want to see what is on disk now.

```bash
autoanimedownloader config set keep_removed_release_cards false
```

#### Recently Downloaded Shelf

`recent_shelf_days` · default `3` (days)

How many days a finished download stays on the Status page's Recently downloaded shelf while you
haven't watched it. `0` turns the shelf off; at most `30`.

Example: you watch on weekends; keep a week.

```bash
autoanimedownloader config set recent_shelf_days 7
```

**First Steps** (Show again) brings back the intro card on the Status page. It is stored in your
browser, not in `config.json`.

### Torrent Search (advanced)

#### Minimum Seeders

`min_seeders` · default `3`

Torrents with fewer seeders are dropped from the search results. The default blocks dead torrents
(which otherwise get picked when they are the only result) and single-seeder ones, which stall
often. `0` = no floor.

Example: an old show where only one seeder is left is better than nothing.

```bash
autoanimedownloader config set min_seeders 1
```

#### Max Search Pages

`max_search_pages` · default `5`

The most Nyaa result pages one search reads. A search only reads the next page while it has few
usable results, so most searches still read one page. `1` or less = page 1 only.

Example: a long-running show whose older packs sit deep in the results.

```bash
autoanimedownloader config set max_search_pages 10
```

#### Wait Between Nyaa Requests

`nyaa_request_delay_seconds` · default `0` (seconds)

The minimum gap between two Nyaa page requests, across every anime searched at once. Raise it if
Nyaa answers with errors or Cloudflare checks; a check then takes longer.

Example: Nyaa keeps answering with errors during your checks.

```bash
autoanimedownloader config set nyaa_request_delay_seconds 2
```

#### Episode Retry Limit

`episode_retry_limit` · default `5`

How many magnet links an episode tries, best first, before it waits for the next check. At least
`1`.

Example: episodes often fail to start; try more releases per check.

```bash
autoanimedownloader config set episode_retry_limit 8
```

#### Max Batch Torrent Size

`max_batch_torrent_size_gb` · default `100` (GiB)

Packs above this size are dropped from the search results. It is the only ceiling on packs: about
40 GiB fits a 26-episode 1080p pack, and 100 GiB deliberately doesn't fit a full One Piece pack (a
partial one does). `0` = no limit.

Example: you want BD packs of long shows and have the space.

```bash
autoanimedownloader config set max_batch_torrent_size_gb 300
```

#### Max Episode Torrent Size

`max_episode_torrent_size_gb` · default `5` (GiB)

Single-episode torrents above this size are dropped from the search results. `5` passes a WEB-DL
(about 1.4 GB) and a good BD encode (about 2 GB) and blocks a BDRemux. `0` = no limit.

Example: you only want small WEB releases.

```bash
autoanimedownloader config set max_episode_torrent_size_gb 2
```

An episode you download by hand (its **Download** button, or **Choose torrent**) skips Max Episode
Torrent Size and Minimum Seeders: a click is your choice. The whole-title picker still applies the
limits, and Min Free Disk Space applies to everything.

### Priorities page

#### Download queues

Stored in `queues.json` next to `config.json`, not in `config.json`, so `config set` doesn't reach
them; the CLI has its own `queues` commands.

The app starts with one queue, **Default**, which holds every anime you haven't put in another
queue (the ones from your AniList and MyAnimeList lists included). A queue you add starts with a
copy of the default ranking. The order is the **search** order: each check searches every anime of
the first queue, then the second, so a later queue waits for the earlier ones. Downloads don't
wait: they share [Max Concurrent Downloads](#max-concurrent-downloads), whatever their queue. Each
queue ranks releases with its own lists (below); the Block list is shared by all queues. Deleting
a queue sends its anime back to Default, which can't be deleted.

An anime is put in a queue on the Add anime screen (**Queue**) or on its page
([Download queue](#download-queue)).

Example: the shows you watch the day they air should be searched first, and in 720p to save space.

```bash
autoanimedownloader queues add "Watch first"
autoanimedownloader queues order watch-first default
autoanimedownloader queues priorities get watch-first --json > top.json   # edit resolutions
autoanimedownloader queues priorities set watch-first top.json
autoanimedownloader animes queue 154587 watch-first
```

#### The ranking lists

`priorities.criteria_order`, `priorities.fansubs`, `priorities.resolutions`, `priorities.sources`,
`priorities.codecs`, `priorities.audio`, `priorities.ignore_list`

The Priorities page ranks Nyaa releases. Each list is an order: higher in the list is better, and
an item not in the list ranks last. With two queues or more, **Ranking of queue** picks which
queue's lists you edit. **Reset this list** and **Reset all** bring back the defaults
(`autoanimedownloader priorities defaults` prints them).

| List | Default | Applies to |
|---|---|---|
| **Criteria order** (`criteria_order`) | `uncensored`, `resolution`, `health`, `source`, `codec`, `fansub`, `audio`, `size` | the order the criteria below are compared in |
| **Fansubs** (`fansubs`) | `subsplease`, `erai-raws`, `judas`, `toonshub`, `asw`, `ember`, `hd-zone`, `kamig`, `remix`, `aniverse`, `dub`, `raw` | a group's name found anywhere in the release name |
| **Resolutions** (`resolutions`) | `1080p`, `720p`, `1440p`, `2160p`, `480p` | every release (`4k` reads as `2160p`) |
| **Source** (`sources`) | `bdremux`, `bd`, `bdrip`, `web-dl`, `webrip`, `tv`, `dvd`, `hdtv` | movies and finished series only; a name with no source counts as `web-dl` |
| **Codec** (`codecs`) | `h.264`, `hevc`, `av1`, `xvid` | every release; a name with no codec ties |
| **Audio** (`audio`) | `flac`, `dts-hd`, `truehd`, `ddp`, `dts`, `aac`, `ac3`, `mp3` | movies only |
| **Block list** (`ignore_list`) | `[dub]`, `[raw]`, `[hardcoded]`, `[hc]`, `re-encode` | a release whose name contains one is dropped, never ranked |

The other criteria: **uncensored** prefers an uncensored release, **health** compares seeders in
three bands (under 10, 10 to 49, 50 and more), and **size** prefers the smaller of two releases of
the same resolution.

The Codec list has two one-click presets: **I prefer compatibility** (H.264 first: plays in any
player with no transcode, subtitles stay soft) and **I prefer smaller files** (AV1 and HEVC first:
up to half the size, but a browser has to transcode them).

Example: you'd rather have smaller files.

```bash
autoanimedownloader config set priorities.codecs av1,hevc,h.264,xvid
```

Example: you never want releases from one group.

```bash
autoanimedownloader config set priorities.ignore_list "[dub],[raw],[hardcoded],[hc],re-encode,[somegroup]"
```

**How two releases get ranked.** Episode 5 of a show still airing, with the default lists. For an
episode of an airing show the app compares uncensored, resolution, health, codec, fansub and size
(source and audio don't apply):

| | A | B |
|---|---|---|
| Name | `[SubsPlease] Show - 05 (1080p) [A1B2C3D4].mkv` | `[Erai-raws] Show - 05 [1080p][HEVC][Multiple Subtitle]` |
| Seeders | 40 | 300 |

1. Uncensored: neither says so, a tie.
2. Resolution: both 1080p, a tie.
3. Health: A is in the 10 to 49 band, B in the 50-and-more band. **B wins**, and the comparison
   stops there.

With 120 seeders on A instead, both are in the same band. Codec: A's name has no codec, so it
ties. Fansub: SubsPlease is above Erai-raws. **A wins**.

### Notifications page

#### Webhooks

`notifications.webhooks` · default none

Each webhook is an HTTP request the app sends when an event happens:

| Field | Key | What it holds |
|---|---|---|
| **Name** | `name` | how you refer to it (`notifications test <name>`) |
| **Method** | `method` | `POST`, `GET` or `PUT` |
| **URL** | `url` | the address; takes template variables |
| **Headers** | `headers` | name and value pairs; values take template variables |
| **Body** | `body` | the request body; takes template variables |
| **Events** | `events` | which events send it (table below); with none it never fires |
| **Which animes** | `anime_sources` | `anilist:<user>`, `mal:<user>`, `standalone`; nothing checked = every anime |

**Use preset** fills the form for ntfy, Gotify, Discord, Telegram, Pushover, Slack, Apprise,
Jellyfin or Plex. The Jellyfin and Plex presets don't send a message: they start a library scan on
**Download completed** and **Episode deleted**. **Which animes** only sends a webhook for the anime
on the checked accounts' lists (any list status) or added by hand; the system events always go
through. **Test** sends a sample (it ignores Which animes); **Preview** shows what would be sent
and sends nothing.

Example: a Discord channel for downloads and problems. Pick the Discord preset and paste your
Discord webhook URL. In `config.json` it looks like this:

```json
{
  "name": "Discord",
  "url": "https://discord.com/api/webhooks/123/abc",
  "method": "POST",
  "headers": {"Content-Type": "application/json"},
  "body": "{\"content\":\"**{{title}}**\\n{{message}}\"}",
  "events": ["new_episode", "download_failed", "download_completed", "episode_deleted",
             "episode_stuck", "library_missing", "daemon_crashed", "check_failing",
             "update_available", "disk_low"]
}
```

Example: Jellyfin scans its library as soon as an episode lands. Pick the Jellyfin preset, put in
Jellyfin's address and an API key (Jellyfin's Dashboard, API Keys):

```json
{
  "name": "Jellyfin",
  "url": "http://192.168.1.10:8096/Library/Refresh",
  "method": "POST",
  "headers": {"X-Emby-Token": "your-jellyfin-api-key"},
  "body": "",
  "events": ["download_completed", "episode_deleted"]
}
```

From the CLI, edit the webhooks as a file. `get` prints their URLs and headers, which can hold
tokens:

```bash
autoanimedownloader notifications get --json > notifications.json
autoanimedownloader notifications set notifications.json
autoanimedownloader notifications preview Discord   # shows the request, sends nothing
autoanimedownloader notifications test Discord      # sends a sample
```

#### Events

A new webhook starts with the Episodes events, **Episode not downloading for 2 days** and the
System events; the Jellyfin and Plex presets start with Download completed and Episode deleted
only. The other Library events are reminders you opt into.

| Label | Event | Sent when |
|---|---|---|
| **Episodes** | | |
| New episode detected | `new_episode` | a torrent for a new episode was found and is being added |
| Download failed | `download_failed` | the torrent client refused every release, or a torrent failed or stalled |
| Download completed | `download_completed` | an episode reached the library |
| Episode deleted | `episode_deleted` | episodes were removed from the library |
| **Library** | | |
| Anime hit the episode limit | `episode_limit_reached` | an anime reached [the episode limit](#limit-episodes-per-anime) |
| Episode not downloading for 2 days | `episode_stuck` | an episode has had a problem (no torrent found, too big, no seeders, disk full…) for 48 hours |
| Finished anime fully in the library | `season_complete` | every episode you haven't watched of a finished anime is in the library |
| Next episode unwatched for 30 days | `unwatched_backlog` | the next episode to watch has waited 30 days |
| Weekly digest of watched files still on disk | `leftover_files` | weekly, while files you are done with are still on disk |
| **System** | | |
| Library folder is gone | `library_missing` | the library folder vanished or was replaced (an unmounted drive) |
| Daemon crashed | `daemon_crashed` | at start, when the previous run crashed |
| Checks are failing (and back to normal) | `check_failing` | checks have failed for 30 minutes and twice in a row, and again when they recover |
| New version available | `update_available` | a newer release is out ([Check for Updates](#check-for-updates)) |
| Disk almost full / downloads stopped | `disk_low` | free space is under twice Min Free Disk Space, or downloads were refused for lack of space |

The full rules (when each one repeats, what it says) are in the
[Config Reference](../agents/config.md#webhook-events).

#### Template variables

Usable in the URL, header values and body:

| Variable | Value |
|---|---|
| `{{title}}` | a short label, like "New episode detected" |
| `{{message}}` | a full sentence with the anime and episode; with several events, one line each |
| `{{anime_name}}` | the anime's title |
| `{{episode}}` | the episode number; empty for a pack |
| `{{reason}}` | why a download failed; for some alerts, their level or cause |
| `{{anime_id}}` | the AniList id |
| `{{torrent_hash}}` | the torrent's info hash |
| `{{count}}` | how many episodes the message covers |
| `{{timestamp}}` | the time, like `2026-11-27 18:30` |
| `{{version}}`, `{{url}}` | the new version and its release page (New version available only) |

#### Batch window (seconds)

`notifications.batch_window_seconds` · default `60`

Groups the events of the same window into one webhook. `0` sends one per event. Useful with
rate-limited services like ntfy.sh: filling a new library can fire dozens of events at once.

Example: you'd rather get one message every two minutes.

```bash
autoanimedownloader config set notifications.batch_window_seconds 120
```

### Per anime

These live on the anime page and the Add anime page. None of them is in `config.json`: the app
keeps them in its own files in the data folder (the queue in `queues.json`).

#### Adding an anime by hand

The **Add anime** page searches AniList and adds a title as a standalone anime, tracked without
any list. **Include unreleased** shows titles that haven't aired yet. With two queues or more,
**Queue** picks its [download queue](#download-queues). Add first looks for whole-title torrents
and opens the [torrent picker](#replace-torrent-and-choose-torrent) when it finds some.

Example: a movie that isn't on any of your lists.

```bash
autoanimedownloader animes search "kimi no na wa"
autoanimedownloader animes add 21519
autoanimedownloader animes add 21519 --queue watch-first   # straight into a queue
```

#### Watched episodes

`progress` · standalone anime only

A standalone anime has no list to say what you've watched, so you set it: **Watched episodes**,
or **Watched up to here** on an episode. Raising it lets the usual deletion rules remove what is
behind it, unless downloads are kept forever. A list anime always takes its progress from the list.

Example: you've watched the first 12 episodes of a show you added by hand.

```bash
autoanimedownloader animes settings 154587 progress 12
```

#### Download queue

Shown with two queues or more. The queue this anime searches in and ranks with; changing it saves
at once.

```bash
autoanimedownloader animes queue 154587 watch-first
autoanimedownloader animes queue 154587 default     # back to Default
```

#### Download folder

`root` · shown in direct mode with two download folders or more

The [download folder](#download-folders) this anime's new torrents go to first; when it is full
they go to the next one. **Fill order (default)** removes the choice.

Example: keep a big long-runner on the hard disk.

```bash
autoanimedownloader animes settings 21 root hdd
autoanimedownloader animes settings 21 root none    # back to the fill order
```

#### Shoko match

`shoko` · shown with Shoko on

The **Shoko** panel says which Shoko series the anime matched. A partial match, where the series
shares the MyAnimeList id but the episodes don't line up, is confirmed by hand with the series id
and an offset (the Shoko episode number of this anime's episode 1, minus 1). **Not in Shoko**
overrides a wrong match; **Back to automatic** undoes your choice.

Example: Shoko lists the whole show as one series (id 3), and this AniList entry's episode 1 is
that series' episode 13.

```bash
autoanimedownloader animes settings 154587 shoko 3 12
autoanimedownloader animes settings 154587 shoko none   # not in Shoko
autoanimedownloader animes settings 154587 shoko auto   # back to automatic
```

#### Episodes in Shoko that a pack brings

`shoko_pack_episodes` · shown with Shoko on

This anime's own [Download Full Megapacks](#download-full-megapacks): **Default**, **Download
them** or **Remove them**.

```bash
autoanimedownloader animes settings 154587 shoko-packs remove
autoanimedownloader animes settings 154587 shoko-packs default
```

#### Replace torrent and Choose torrent

**Replace torrent** (the page header) opens the torrent picker with the whole-title releases,
best first. A row whose name holds several seasons or the movies carries a **Megapack** chip, and
its **What goes where** table shows which season, movie or OVA each file belongs to; the whole
torrent still downloads. Picking one deletes the anime's downloaded episodes and downloads it in
their place. **Choose torrent** (an episode's menu) does the same for one episode, with all its
releases, the ones above the size and seeder limits included.

Example: the app picked a 720p release and you want the 1080p BD pack.

```bash
autoanimedownloader animes candidates 154587            # the picker's list
autoanimedownloader animes candidate 154587 1790123     # one row's files and What goes where
autoanimedownloader animes episode-candidates 154587 3  # one episode's releases
```

#### A manual magnet

**Replace** on an episode takes a magnet link you paste; the picker's magnet strip does the same for
the whole anime.

Example: a friend sent you the magnet of a release that isn't on Nyaa.

```bash
autoanimedownloader animes replace-episode 154587 3 --magnet "magnet:?xt=urn:btih:..." --yes
autoanimedownloader animes replace 154587 --magnet "magnet:?xt=urn:btih:..." --yes
```

#### Blocking an episode

**Delete** on an episode removes it and blocks it: it is marked **Blocked** and won't be
downloaded again. **Back to automatic** lifts the block.

Example: episode 7 is a recap you never want.

```bash
autoanimedownloader animes delete 154587 7 --yes
autoanimedownloader animes release 154587 7     # back to automatic
```

#### Blacklist

The page header's **Delete**. An anime you added by hand is removed from the app (its files too,
unless you untick **Delete downloaded files**). An anime from a list can't be removed, since the
list would bring it back: the dialog offers **Delete downloaded files** and **Blacklist it**. A
blacklisted anime is no longer checked or downloaded, its unfinished downloads are paused, and its
files stay. The **Blacklist** page lists them with **Remove from blacklist**.

Example: your partner's list has a show you never want on your disk.

```bash
autoanimedownloader blacklist add 154587
autoanimedownloader animes remove 154587 --yes --blacklist --delete-episodes   # files too
autoanimedownloader blacklist remove 154587
```

### Docker

#### First-start variables

Read only by the container's entrypoint, which writes the first `config.json`. Once that file
exists they do nothing, unless `FORCE_CONFIG_UPDATE` is set, which rewrites the **whole** file and
loses everything set in the web UI or the CLI.

| Variable | Default | Writes |
|---|---|---|
| `COMPLETED_ANIME_PATH` | `/app/downloads/completed` | `completed_anime_path` |
| `ANILIST_USERNAMES` | empty | `anilist_usernames` (comma-separated) |
| `MAL_USERNAMES` | empty | `mal_usernames` (comma-separated) |
| `EXCLUDED_LISTS` | empty | `excluded_lists` (comma-separated) |
| `CHECK_INTERVAL` | `10` | `check_interval` |
| `MAX_EPISODES_PER_ANIME` | `12` | `max_episodes_per_anime` |
| `EPISODE_RETRY_LIMIT` | `5` | `episode_retry_limit` |
| `DELETE_WATCHED_EPISODES` | `true` | `delete_watched_episodes` |
| `QBITTORRENT_URL` | empty | when set: `torrent_client` qBittorrent, direct mode, `qbittorrent_url`, and the app manages that qBittorrent's settings |
| `FORCE_CONFIG_UPDATE` | unset | rewrites an existing `config.json` |

`MAX_EPISODES_PER_ANIME` defaults to `12` here, unlike the app's own default (off). In direct mode
it only acts once Delete Downloads Automatically is on. `ANILIST_USERNAME` and `EXCLUDED_LIST`
(singular) are still read as fallbacks. The compose file already sets `COMPLETED_ANIME_PATH` to
`/library`, its mounted library.

Example: the first start should already follow your AniList account. Add it to the daemon's
environment in `docker/docker-compose.yml`:

```yaml
services:
  daemon:
    environment:
      ANILIST_USERNAMES: me
```

#### The compose file

`docker/docker-compose.yml` reads these from `docker/.env` or the shell:

| Variable | Default | What it sets |
|---|---|---|
| `AAD_DOCKER_DIR` | `../runtime/docker` | the host folder holding `data/` and `library/`; both must exist |
| `AAD_WEB_PORT` | `8091` | the host port of the web UI, on `127.0.0.1` only |
| `AAD_UID`, `AAD_GID` | `1000` | the user the app runs as; it must own both folders |
| `TZ` | `UTC` | the timezone of the log timestamps |
| `COMPOSE_PROFILES` | unset | `qbittorrent` adds a qBittorrent service next to the app |
| `AAD_QBIT_PORT` | `8093` | that qBittorrent's WebUI port, the same inside and outside |
| `AAD_QBITTORRENT_URL` | empty | passed as `QBITTORRENT_URL` on the first start (`http://qbittorrent:8093`) |

Example: run on port 8100 with the data on a big drive, with qBittorrent.

```bash
# docker/.env
AAD_DOCKER_DIR=/mnt/big/aad
AAD_WEB_PORT=8100
COMPOSE_PROFILES=qbittorrent
AAD_QBITTORRENT_URL=http://qbittorrent:8093
```

Details, including qBittorrent's first password: [Environment Variables](../agents/environment.md).

### Advanced: environment variables

Read by the app itself, inside or outside Docker:

| Variable | Default | What it does |
|---|---|---|
| `PORT` | `:8091` | the address the web UI and API listen on |
| `ENVIRONMENT` | `dev` | `dev` writes readable console logs; any other value writes JSON logs |
| `AAD_DISABLE_DHT` | unset | `1` runs the built-in client without DHT, for two daemons on one machine; a magnet whose trackers are down then finds no peers |
| `NYAA_URL` | `https://nyaa.si` | another Nyaa address, such as a mirror |
| `ANILIST_API_URL` | `https://graphql.anilist.co` | another AniList API address |
| `GITHUB_API_URL` | `https://api.github.com` | where the update check asks for the latest release |
| `AAD_API_ENDPOINT` | `http://localhost:8091` | the CLI's daemon address (same as `--endpoint`) |

Example: the app should listen on port 9000.

```bash
PORT=:9000 autoanimedownloader-daemon
autoanimedownloader --endpoint http://localhost:9000 status
```

### Not in config.json

- **Theme** (Light, Dark, System) and **First Steps** are kept in your browser.
- **Download queues** are in `queues.json`; per-anime settings in the data folder.
- Old keys move on their own: `anilist_username` and `excluded_list` (singular) become
  `anilist_usernames` and `excluded_lists` when the config loads. `anime_ids_are_media_ids` is an
  internal marker: don't edit it. `save_path` and `min_free_disk_percent` no longer exist and are
  dropped.

## Planned (designed, not built)

<!-- Move an option up to "In the app today" in the change that ships it. -->

> **Nothing in this part is in the app yet.** It comes from two designs:
> [release filters](../superpowers/specs/2026-11-23-release-filters-design.md) and
> [megapack downloads](../superpowers/specs/2026-11-23-megapack-downloads-design.md). Names,
> defaults and keys can change before they ship. Values marked *proposed* are not decided yet.

### Lists made in the app

A **Lists** page to make your own lists, separate from AniList's and MyAnimeList's but built from
them. A list mixes anime picked one by one, whole account lists (an AniList status or custom list,
a MyAnimeList status list), "added by hand" anime, and exceptions it leaves out. Filters (next)
apply to lists.

Example: a list "Kids — dubbed" made of your second account's custom list "Kids".

### Filters

A **Filters** page next to Priorities, modeled on autobrr. Where Priorities only ranks, a filter
can refuse: a release that matches no filter of its anime is not downloaded. Each filter has a
name, an on/off switch, the lists it covers, a priority, the kinds it takes (episode, pack, movie)
and any of these fields, each with **match** and **except** (except wins):

| Field | Values |
|---|---|
| resolutions | `1080p`, `720p`, `2160p`… |
| sources, services | `web-dl`, `bd`, `bdremux`…; `cr`, `nf`, `amzn`… |
| video codecs, bit depths | `h.264`, `hevc`, `av1`…; `8`, `10` |
| audio codecs, channels, languages | `aac`, `flac`, `opus`…; `2.0`, `5.1`; `ja`, `en`, `multi` |
| subtitle languages, subtitles | `en`, `pt-br`…; `hardsub`, `softsub` |
| containers, release groups, releases | `mkv`, `mp4`; group names; the whole name, with wildcards or a regex |
| other | `uncensored`, `repack`, `batch`, `trusted`, `remake` |
| ranges | bitrate (estimated from size and episode length), size per episode, seeders |

Example: Japanese audio and English subtitles in 1080p, never a hardsub, for everything you watch.

- **Priorities become tiers.** A release's tier is the priority of the best filter it matches.
  Tiers rank first; today's Criteria order breaks ties inside a tier.
- **When the name doesn't say.** Many names don't state audio or subtitles. Four modes, a default
  in Settings and a choice per filter: smart defaults plus known groups (the default), smart
  defaults only, "not stated passes", "not stated fails". Smart defaults: Japanese audio, English
  subtitles, WEB-DL when no source is named.
- **Known groups.** An editable table of groups whose releases always look the same. It ships with
  SubsPlease only (Crunchyroll WEB-DL, H.264, AAC 2.0, Japanese audio, English softsubs).
- **Only accept after.** Per filter, off by default: since airing (the episode aired that long
  ago) and since upload (the release has been on Nyaa that long), in hours or days.

Example: you prefer the dual-audio BD, but not forever. A filter for dual-audio BD at priority 20,
and a catch-all at priority 0 that accepts anything 14 days after airing.

### Fewer Nyaa requests

- **The daily sweep.** Once a day, the app reads the previous day's Nyaa uploads (two or three
  pages) and matches them against your anime, instead of searching every anime on every check. A
  new episode is found up to a day after release. After the app was off, it catches up 14 days
  (*proposed*).
- **The search queue.** The per-anime searches still needed (an anime just added, finished shows,
  an episode the sweep missed 2 days (*proposed*) after airing, upgrade searches) wait in a queue
  with a **budget per period** (searches per day, week, month or year) and **time windows** (hours,
  days). It can also hold back the **downloads** it finds; by default they start at once. The
  picker, manual downloads and `debug-anime` skip it.

This search queue is not today's [download queues](#download-queues): those set the order anime are
searched in; this one would set when searches may run at all.

Example: at most 200 searches a day, only between 2 and 6 in the morning.

### Following a release

**Follow this release**, when you pick one by hand: later episodes come from the same release line,
meaning the same group and key tags (resolution, source and service, audio languages). An advanced
option picks exactly which tags must match. If the line hasn't brought an episode 7 days
(*proposed*) after it aired, the normal filters take over for that episode.

Example: you picked `[ToonsHub] … 1080p NF WEB-DL DUAL`; every next episode comes from ToonsHub,
1080p, Netflix WEB-DL, dual audio.

### Upgrades

- **Always upgrade.** An episode whose release sits below the best tier its filters can give keeps
  being looked for, until it holds a release from its top filter (the cutoff) or a followed line.
  An episode with no filters never upgrades.
- **Replace, Keep both or Off**, with a per-anime choice. Keep both downloads the better release
  as well and keeps the old one.
- **Pins.** Pin an episode or a whole torrent on the anime page, and it is never upgraded
  (`torrents pin|unpin` in the CLI). A torrent you pick by hand starts pinned (*proposed*).
- **The Replaced page.** What replaced what, and when the old files go: kept **3 days** by
  default, settable in hours or days. Remove now, keep, or undo.
- **Approval**: automatic (*proposed* default), approve before downloading, or approve before
  removing.

Example: you have the WEB release of a finished show and a BD appears that your top filter wants.
It downloads, the WEB release moves to Replaced, and it is removed three days later.

### What the torrents section reads from the file

The anime page's torrent cards are built; the file reading isn't. Planned: each downloaded video's
real audio and subtitle tracks, read from the `.mkv`, with a warning when the file contradicts the
name ("Dual-Audio" with one audio track).

### Megapacks

A megapack is one torrent holding several seasons, often with the movies and OVAs. Today the
picker shows **What goes where** and the whole torrent downloads. Planned:

- **File selection** (qBittorrent only): tick and untick files and whole entries before and after
  the download. Everything the mapping places is ticked by default; creditless openings and
  unplaced files are not. Ticking more later downloads only the new files.
- **Download related seasons**, on by default: off, only the entries you track are ticked.
- **Search order**: a megapack a filter accepts, then a season pack, then single episodes.
- **The megapack queue**: a megapack above 100 GiB (*proposed*) waits in a queue of its own, with
  its own budget and time windows, starting at one a week (*proposed*). Only anime on your
  app-made lists can use it.
- **Franchise groups** on the anime page: every season, movie and OVA of the franchise, each with
  a **Track** switch (on by default), and **Download the whole series**.
- **Season folder names**: a megapack's folders kept as the torrent lays them out, or renamed
  `S01`, `S02`… (a planned `rename_pack_season_folders`).

Example: you track Kimetsu no Yaiba season 3 and the best release is a `S01-05` pack. You tick
seasons 3 to 5 and only those download.

### qBittorrent as the only client

The plan is for qBittorrent to replace the built-in client. When that ships, hardlink mode goes
(direct mode becomes the only [library mode](#write-downloads-straight-to-the-library)), Disable
Seeding is rewritten around qBittorrent, and the Incomplete Downloads Folder becomes qBittorrent's
own folder for unfinished torrents, as it already is with qBittorrent today. Until then both
clients are supported.

## See Also

- [WebUI Guide](webui.md) — every page and action
- [CLI Guide](cli.md) — every command
- [Config Reference](../agents/config.md) — types, validation and decisions behind each key
- [Environment Variables](../agents/environment.md) — Docker and development variables
