# Options guide with examples

Found on 2026-11-23 while specifying release filters and megapacks. Not started. Delete this file
when the guide exists or the idea is dropped.

## What

A user guide that explains **every option in the app with an example of using it**: what it does,
its default, and a short real case ("you want X → set Y"). The user's words: "create a
documentation that will use examples to explain the usage of every option there is in the app",
and "document all the options we are adding".

Proposed file: `docs/guides/options.md`, linked from `docs/guides/webui.md` ("See Also") and from
the README.

## Ground rules

- Plain English, short sentences, an example per option. Name each option the way the **web UI**
  labels it, then its `config.json` key and the CLI line (`autoanimedownloader config set <key>
  <value>`).
- Two parts: **in the app today**, and **planned** (designed, not built), the second clearly
  marked so nobody looks for a setting that isn't there yet. Move an option from planned to today
  in the change that ships it.
- `docs/agents/config.md` stays the reference for contributors (types, validation, decisions);
  the guide is for users and links to it rather than repeating the internals.

## Sources

| what | where |
|---|---|
| every config key, default, validation | `docs/agents/config.md` (Fields, Direct Library Mode, Validation, Per-Anime Settings, Webhook variables and events) |
| the authoritative key list | the `json:"…"` tags of `Config` in `src/internal/files/filemanager.go` |
| the labels and hints users see | `src/internal/frontend/messages/en.json`: `config_label_*`, `config_hint_*`, `config_section_*`, `priorities_*`, notification keys |
| pages and actions | `docs/guides/webui.md` |
| CLI syntax | `docs/guides/cli.md` (`config get/set`, `priorities defaults`, `notifications test/preview`) |
| Docker and environment variables | `docs/agents/environment.md` (entrypoint variables, the compose file's `AAD_*` variables, `PORT`, `ENVIRONMENT`, service overrides) |
| planned options | `docs/superpowers/specs/2026-11-23-release-filters-design.md`, `docs/superpowers/specs/2026-11-23-megapack-downloads-design.md` |

## What it must cover

**In the app today** (35 `config.json` keys on 2026-11-23, plus the lists inside them), grouped as
the Config page groups them:

- **Library:** `completed_anime_path`, `library_mode` (hardlink / direct, with the folder layout of
  each), `rename_files_for_jellyfin`, `episode_numbering`, `incomplete_files`, `incomplete_path`,
  `incomplete_min_free_disk_gb`, `auto_delete_downloads`, `min_free_disk_gb`.
- **Lists:** `anilist_usernames`, `mal_usernames`, `download_statuses`, `download_media_statuses`,
  `delete_statuses` (the AND rule across accounts against download's OR), `excluded_lists`,
  `delete_watched_episodes`, `watched_episodes_to_keep`.
- **Downloads:** `check_interval`, `max_episodes_per_anime` (and why it never limits packs),
  `episode_retry_limit`, `max_concurrent_downloads`, `disable_seeding`, `check_for_updates`.
- **Torrent search:** `max_batch_torrent_size_gb`, `max_episode_torrent_size_gb`, `min_seeders`,
  `max_search_pages`, `nyaa_request_delay_seconds`.
- **Priorities:** `criteria_order`, `fansubs`, `resolutions`, `sources`, `codecs` (and the two
  presets), `audio`, `ignore_list` — with one worked example of how two releases get ranked.
- **Notifications:** webhook presets (`name`, `url`, `method`, `headers`, `body`, `events`,
  `anime_sources`), `batch_window_seconds`, every event, every template variable; examples for a
  Discord message and a Jellyfin library scan.
- **Per anime:** adding a standalone anime, its manual progress, blocking an episode, replacing a
  torrent through the picker, a manual magnet.
- **Docker:** the entrypoint variables and the compose file's `AAD_DOCKER_DIR`, `AAD_WEB_PORT`,
  `AAD_UID`/`AAD_GID`, `TZ`.
- **Advanced:** `PORT`, `ENVIRONMENT`, `AAD_DISABLE_DHT`, `NYAA_URL`, `ANILIST_API_URL`,
  `GITHUB_API_URL`.
- Leave out the internal markers (`anime_ids_are_media_ids`) and the legacy keys
  (`anilist_username`, `excluded_list`) except one line saying they migrate on their own.

**Planned** (from the two specs):

- app-made lists; filters and their fields; priorities as tiers; "when the name doesn't say" modes;
  the known-groups table; `accept_after` (since airing, since upload);
- the daily sweep; the search queue (budget per period, time windows, holding back downloads);
- following a release (group and key tags, the per-tag override);
- upgrades: the cutoff, replace / keep both / off, pins, the Replaced page (keep for 3 days, in
  hours or days), approval before downloading or before removing;
- the downloaded-torrents section and its history setting;
- megapacks: search order, download related seasons, the megapack queue, franchise groups and
  their Track switch, file selection.

## Watch for

**Rain is being dropped for qBittorrent** (the user's decision, 2026-11-23, recorded in the
megapack spec). Hardlink mode, `disable_seeding`, `incomplete_path` and the `.torrents` folder
change or disappear with it. If the guide is written before that switch, say which options it
affects; if after, document the qBittorrent settings instead.
