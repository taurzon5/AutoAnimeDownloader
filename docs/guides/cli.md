# CLI Guide

The CLI (`autoanimedownloader`) drives the daemon over the REST API. A bare
`autoanimedownloader` prints the command tree. `--help` on every command states
`Usage`, `ArgsUsage`, flags and defaults.

## Global flags

| Flag | Env | Behavior |
|---|---|---|
| `--endpoint` / `--url` | `AAD_API_ENDPOINT` | API URL (default `http://localhost:8091`) |
| `--json` | | stdout is the API `data` value (no `{success,data,error}` envelope). stderr is empty on success |
| `--verbose` / `-v` | | verbose output. Version is `--version` |

Put global flags before the command: `autoanimedownloader --json status`.

Default output (no `--json`) is a human table or a one-line message.

| Outcome | Exit | Output |
|---|---|---|
| success | 0 | table / message; with `--json`, the API data |
| API / daemon error | 1 | stderr text; with `--json`, stdout `{"error":{"code":"…","message":"…"}}` using the API's `code` |
| destructive without `--yes` | 2 | code `CONFIRMATION_REQUIRED`, message lists the flags. No stdin, no prompt |
| daemon unreachable | 1 | code `DAEMON_UNREACHABLE`. `report` and `report crash-ack` never take this path |

## Process

```bash
autoanimedownloader start
autoanimedownloader stop
autoanimedownloader open
```

`start` / `stop` manage the daemon process (systemd user unit or PID file). `open` opens the web UI.

## Loop

```bash
autoanimedownloader loop start
autoanimedownloader loop stop
```

Starts or stops the verification loop on the running daemon.

## Status

```bash
autoanimedownloader status
autoanimedownloader --json status
autoanimedownloader last-check
autoanimedownloader check
```

`status` prints the full payload: daemon state, last check, disk, AniList health, pending crash,
and the newer version the daemon's update check found (`latest_version`, `latest_version_url`;
empty when there is none or `check_for_updates` is off). When there is one, the table is followed
by `Update available: v2.11.0 (<release page>)`; `--json` carries just the two fields. It also
names the torrent client the daemon runs (`torrent_client`): the client is picked when the daemon
starts, so after `config set torrent_client …` `torrent_client_restart` is true and the table is
followed by `Restart needed: the daemon still runs the builtin client; restart it to switch`.
`last-check` is why the last automatic pass did not download. `check` triggers a pass now.

## Configuration

```bash
autoanimedownloader config get
autoanimedownloader config set anilist_usernames me,other
autoanimedownloader config set check_interval 15
autoanimedownloader config set completed_anime_path "/mnt/Anime, Series"
autoanimedownloader config set notifications.batch_window_seconds 30
autoanimedownloader config set priorities.fansubs SubsPlease,Erai-raws
autoanimedownloader priorities defaults
autoanimedownloader priorities get --json > priorities.json
autoanimedownloader priorities set priorities.json
```

`config get` prints **every** JSON key `config.json` serializes. `config set` accepts any of those
keys by JSON name (case and underscores ignored). A key that already holds a list accepts
comma-separated values; anything else is JSON when it parses, otherwise a string.

A dotted key sets one field inside a nested object and keeps the rest of it. Setting the whole
object (`config set notifications '{…}'`) still works, but replaces it: a field left out of the JSON
is gone, webhooks included. A value under `notifications` / `notifications.webhooks` isn't echoed
back after saving: webhook URLs and headers hold tokens. Neither are `qbittorrent_password`,
`qbittorrent_api_key` and `shoko_api_key`, which `config get` shows as a placeholder.

`priorities defaults` is the default torrent priority lists. To change one: `config set priorities.<list> …`.
`priorities get` prints the saved lists; `priorities set <file|->` saves them from JSON (`-` reads
stdin) and nothing else. A list the file leaves out keeps its saved value, as the Priorities page does (`PUT /config/priorities`): unlike `config set`,
it doesn't run the folder and torrent-client checks, so an unplugged download drive doesn't block it.

## Animes

```bash
autoanimedownloader animes
autoanimedownloader animes --account alice
autoanimedownloader animes --account anilist:alice --account mal:bob --unwatched
autoanimedownloader animes --name frieren --type airing --sort name
autoanimedownloader animes search "frieren"
autoanimedownloader animes search "foo" --unreleased
autoanimedownloader animes candidates 154587
autoanimedownloader animes candidates 154587 --query "frieren s01"
autoanimedownloader animes episode-candidates 154587 3   # one episode's releases; download one with replace-episode --magnet
autoanimedownloader animes candidate 154587 1790123
autoanimedownloader animes add 154587
autoanimedownloader animes add 154587 --magnet "magnet:?xt=…"
autoanimedownloader animes add 154587 --magnet "magnet:?xt=…" --query "frieren s01"
autoanimedownloader animes add 154587 --unblacklist   # it is on the blacklist: take it off and add it
autoanimedownloader animes remove 154587 --yes
autoanimedownloader animes remove 154587 --yes --delete-episodes
autoanimedownloader animes remove 154587 --yes --blacklist   # a list anime: stop checking and downloading it
autoanimedownloader animes settings 154587
autoanimedownloader animes settings 154587 progress 12
autoanimedownloader animes settings 154587 shoko 3 12   # Shoko series 3, its episode 13 is this anime's 1
autoanimedownloader animes settings 154587 shoko none   # not in Shoko
autoanimedownloader animes settings 154587 shoko auto   # back to the match from the MAL id
autoanimedownloader animes settings 154587 shoko-packs remove   # a pack's episodes already in Shoko are removed
autoanimedownloader animes settings 154587 shoko-packs default  # back to shoko_full_packs
autoanimedownloader animes settings 154587 root hdd    # new torrents of this anime go to the hdd download folder first
autoanimedownloader animes settings 154587 root none   # back to the fill order
autoanimedownloader animes episodes 154587
autoanimedownloader animes releases 154587   # the torrents that brought its episodes, with their tags
autoanimedownloader animes debug 154587
autoanimedownloader animes download 154587 3
autoanimedownloader animes redownload 154587 3 --yes
autoanimedownloader animes redownload 154587 3 4 5 --yes
autoanimedownloader animes delete 154587 3 --yes
autoanimedownloader animes delete 154587 3 4 5 --yes
autoanimedownloader animes release 154587 3
autoanimedownloader animes replace 154587 --magnet "magnet:?xt=…" --yes
autoanimedownloader animes replace-episode 154587 3 --magnet "magnet:?xt=…" --yes
```

`debug` runs the daemon's `--debug-anime` pass inside the running daemon: it prints which episodes
would be searched and how many magnets each found, downloads nothing, and leaves the full trace in
the daemon log (`logs`). It can take minutes on a long series.

`animes` lists tracked titles (columns follow `AnimeInfo`) with the dashboard's filters, applied by
the CLI to what `GET /api/v1/animes` returns:

| Flag | Default | Behavior |
|---|---|---|
| `--account` | every account | animes that account has in a **download** status. `USER` picks that name on every service it is configured on; `anilist:USER` / `mal:USER` picks one; the username's case doesn't matter. Repeat for several (OR). `episodes_watched` / `episodes_pending` (and `recent`, the Status page's Recents shelf) become **that account's** — with several, the one furthest behind — so `--unwatched` and `--sort watched` answer for it. An account not in the config is an error listing the configured ones. An account whose list the daemon couldn't fetch (AniList/MAL down or rate-limited) carries no anime, so picking it fails with `ACCOUNT_LIST_UNAVAILABLE` instead of printing an empty list; without it picked, the human output warns on stderr (`--json` keeps stderr empty) |
| `--name` | | any title (English, romaji, native, synonyms) contains the text; case and diacritics ignored, like the dashboard search |
| `--unwatched` | false | hides the animes whose released episodes are all watched; one with none released yet stays |
| `--type` | `all` | `airing` (releasing or hiatus), `finished` (finished or cancelled), `standalone`, `blacklisted` |
| `--sort` | `last-download` | `name` (A–Z, Unicode collation: `Émile` sorts with the E's), `watched`, `last-download` (both most first); `--reverse` flips |

`search` is AniList; a term shorter than
3 characters returns an empty list, not an error. `candidates` is the ranked whole-title list the
picker shows (empty `items` is success, not an error). `--query` is a Nyaa `q` and skips title
variants and season/part. `candidates --queue <id>` ranks the list as that download
queue would (by default, the anime's own queue). `candidate <id> <nyaa_id>` is the picker's detail panel: the Nyaa URL and
the files inside the torrent (`--json` also carries the description as HTML). For a megapack, a
torrent whose videos belong to two entries or more of the anime's franchise, it adds the mapping
table: each entry with its episodes (`1-26 of 26`), files and size, the anime asked about marked
`(this anime)`, then the videos no entry takes and why (creditless openings, a special no entry's
name matches, a number past the last episode AniList lists). `--json` carries the `mapping` of any
torrent with two videos or more. `episodes` is followed
by an `On lists:` table — every configured account that has the anime, in any status, with its
progress and score (the anime page's panel). `add --magnet` skips auto-pick and looks the magnet up among the
candidates (pass the same `--query` you used with `candidates`), so it sends the release name, Nyaa id
and size the web picker sends — the daemon reads the episode range from them. A magnet that is not
among them is added with the magnet alone. Flags may follow the id (`animes add 154587 --magnet "…"` is
the same as flags-first). `add` is a standalone anime; on a blacklisted one it fails with
`BLACKLISTED` unless `--unblacklist` takes it off the blacklist first.

`remove` removes an anime added by hand (standalone); `--delete-episodes` deletes its downloaded
episodes too, otherwise the files stay, marked manually managed. An anime a list brings can't be
removed (the list would bring it back): `remove` refuses with `ON_A_LIST` unless you pass
`--delete-episodes` (its episodes are deleted and blocked, so they don't download again) and/or
`--blacklist`. `--blacklist` works on either kind and puts the anime on the blacklist (below).

`delete` and `redownload` take one or more episodes. Several go in one request: `delete` frees a
pack they share and deletes nothing if any of them is not downloaded; `redownload` keeps going past
a failed episode, lists the failures and exits non-zero.

`release` is not destructive and does not take `--yes`.

Download a new anime: `animes search` then `animes add <media_id>`.

## Queues

```bash
autoanimedownloader queues
autoanimedownloader queues add "Watch first"
autoanimedownloader queues order watch-first default
autoanimedownloader queues rename watch-first "Top"
autoanimedownloader queues priorities get watch-first --json > top.json
autoanimedownloader queues priorities set watch-first top.json
autoanimedownloader queues delete watch-first --yes
autoanimedownloader animes queue 154587 watch-first
autoanimedownloader animes add 154587 --queue watch-first
```

Download queues search in their order: each check searches every anime of the first queue, then
the next, so a later queue's searches wait. Downloads share `max_concurrent_downloads` whatever
their queue. Each queue ranks releases with its own priority lists; the ignore list is shared, and
the default queue's lists are the ones `priorities get` shows. `queues` lists the ids in order, with
how many anime each holds (the default queue holds every anime no other queue lists). `add` makes a
queue at the end with a copy of the default ranking; `order` needs every id once; `delete` sends the
queue's anime back to the default queue (the default queue can't be deleted). `queues priorities
set` replaces the lists the JSON names and keeps the others. `animes queue <id> default` moves an
anime back.

## Blacklist

```bash
autoanimedownloader blacklist            # same as blacklist list
autoanimedownloader blacklist add 154587
autoanimedownloader blacklist remove 154587
```

The app's own blacklist: a blacklisted anime is no longer checked or downloaded, its unfinished
torrents are paused, and its files stay; no automatic delete rule touches them. `remove` takes it
off and resumes the torrents the blacklist paused (not one you had paused yourself). It is not
`excluded_lists`, which names AniList custom lists. `animes --type blacklisted` shows both kinds.

## Torrents

```bash
autoanimedownloader torrents
autoanimedownloader torrents files HASH
autoanimedownloader torrents pause HASH
autoanimedownloader torrents resume HASH
autoanimedownloader torrents announce HASH
autoanimedownloader torrents prioritize HASH
autoanimedownloader torrents prioritize HASH1 HASH2
autoanimedownloader torrents delete HASH --yes
autoanimedownloader torrents delete HASH --yes --keep-data --block
autoanimedownloader torrents redownload-missing
autoanimedownloader torrents redownload-missing HASH1 HASH2
autoanimedownloader torrents test
```

`test` hits `POST /torrent-client/test` with the saved settings: it signs in to qBittorrent,
prints its version and Web API version, and says whether qBittorrent sees the library folder (or
what is wrong: too old, a path map that points elsewhere). It also prints how it signed
in (API key or password), whether qBittorrent sees the incomplete folder when one is set,
qBittorrent's own download queue (how many downloads run at a time, "no limit", or "off"), which is
what applies unless `qbittorrent_manage_settings` lets the app set it, and a hint when qBittorrent
uses memory-mapped disk I/O. A failed sign-in or an unreachable
qBittorrent is an error. It saves and changes nothing. With the built-in client there is nothing to
test; set `torrent_client`, `qbittorrent_url` and the login with `config set` first, e.g.
`autoanimedownloader config set qbittorrent_url http://127.0.0.1:8080`.

`redownload-missing` hits `POST /torrents/redownload-missing`: every finished direct-mode torrent
whose library files were deleted outside the app (or whose library folder was replaced) downloads
them again into the same paths — or only the hashes given, sent as `{"hashes":[…]}`. A torrent whose missing files don't fit on disk is skipped and
counted.

One hash on `prioritize` hits `POST /torrents/{hash}/prioritize`. Two or more hits
`POST /torrents/prioritize` with `{"hashes":[…]}` in the order given.

`--keep-data` and `--block` default to false.

## Shoko

```bash
echo -n "$SHOKO_PASSWORD" | autoanimedownloader shoko login --url http://127.0.0.1:8111 --user admin
autoanimedownloader shoko status
autoanimedownloader shoko sync
autoanimedownloader shoko sync --full
```

`login` hits `POST /shoko/login`. It reads the password from standard input: piped, or typed after
the prompt, where it shows as you type. It saves only the address and the API key Shoko returns.
Then turn Shoko on with `config set shoko_enabled true`. `status` shows whether it is on and signed
in, the last sync, how many episodes of followed anime Shoko has, whether it follows Shoko's
changes as they happen (`shoko_watch`), Shoko's import folders as Shoko sees them (a drop source
marked), and the last time the app asked Shoko to import new files. `sync` starts a check pass that
syncs first, and `--full` reads all of Shoko again, so files deleted or moved there stop counting.
A match that needs checking by hand is set with `animes settings <id> shoko …`, above. A pack chosen
for the missing episodes downloads whole, so it also brings the episodes Shoko has. With
`shoko_full_packs` on (the default) the app keeps them as its own; `config set shoko_full_packs
false` removes them instead (skipped with qBittorrent, deleted once downloaded otherwise), and
`animes settings <id> shoko-packs download|remove|default` chooses for one anime. To download into
Shoko, list one of Shoko's import folders among the download folders (direct mode only). `shoko
status` lists Shoko's folders, as Shoko sees them and, through `shoko_path_map`, as the app does,
and says for each download folder whether it is in Shoko. When the two see a folder at different
paths (Docker), map it: `config set shoko_path_map '[{"app":"/mnt/shoko-drop","shoko":"/import"}]'`.
`config set download_roots` replaces the whole list, so name every folder:
`config set download_roots '[{"name":"shoko","path":"/mnt/shoko-drop"},{"name":"library","path":"/mnt/anime"}]'`.
Each download that finishes in a folder in Shoko keeps seeding, and Shoko is asked to import it;
any other folder takes what you download outside Shoko.

## Logs

```bash
autoanimedownloader logs
autoanimedownloader logs --lines 100
autoanimedownloader logs --level error
autoanimedownloader logs --search "nyaa"
NO_COLOR=1 autoanimedownloader logs
```

Reads the daemon log file (last 1000 lines by default).

## Report

```bash
autoanimedownloader report
autoanimedownloader report --output /tmp/aad-report.txt
autoanimedownloader --json report --no-titles --error "…" --context "…" --anime 154587
autoanimedownloader report crash-ack
```

Reads the data dir on disk. The daemon may be dead. `--json` prints `{bundle, environment}`.
`crash-ack` archives `crash-pending.log`; `/status` then clears `pending_crash_at` on the next poll.

## Notifications

```bash
autoanimedownloader notifications test discord
autoanimedownloader notifications preview discord
autoanimedownloader notifications get --json > notifications.json
autoanimedownloader notifications set notifications.json
```

`test` fires a test payload at the webhook named in config. `preview` renders the same sample payload
(method, URL, headers, body) and sends nothing — check a template before it reaches the service.
`get` prints the webhooks and the batch window; `set <file|->` saves them from JSON and nothing else
(`PUT /config/notifications`); a key the file leaves out keeps its saved value, as the Notifications page does. The output of `get` holds the
webhooks' URLs and headers, which can carry tokens.

## Destructive flags

These require `--yes`. Optional flags map 1:1 onto API query params (the param named in parentheses when it differs); absent means `false`:

| Command | Extra flags |
|---|---|
| `animes remove` | `--delete-episodes` (`delete_files`) `--blacklist` |
| `animes redownload` | |
| `animes delete` | |
| `animes replace` | `--magnet` (required) |
| `animes replace-episode` | `--magnet` (required) |
| `torrents delete` | `--keep-data` `--block` |

## Examples

```bash
autoanimedownloader start
autoanimedownloader config set anilist_usernames myusername
autoanimedownloader config set completed_anime_path /path/to/library
autoanimedownloader loop start
autoanimedownloader --json animes
autoanimedownloader animes search "sousou no frieren"
autoanimedownloader animes add 154587
autoanimedownloader check
autoanimedownloader logs --level warn -q retry
```

## Troubleshooting

**Command not found.** Install it on `PATH` (`~/.local/bin/` on Linux).

**`DAEMON_UNREACHABLE`.** `autoanimedownloader start`, then retry. `report` still works.

**`CONFIRMATION_REQUIRED`.** Pass `--yes` (and the optional flags listed in the message).

## See also

- [WebUI Guide](webui.md)
- [Development Guide](development.md)
