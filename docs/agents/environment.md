# Environment Variables

## Application

| Variable | Default | Where | Description |
|----------|---------|-------|-------------|
| `ENVIRONMENT` | `"dev"` | `cmd/daemon/main.go` | Set to `dev` for formatted console logs. Any other value = production JSON logs |
| `PORT` | `":8091"` | `cmd/daemon/main.go`, `daemon/helpers.go` | HTTP listen address. Prefix `:` auto-added if missing |
| `AAD_DISABLE_DHT` | — | `torrents/sessionmanager.go` (`NewSessionManager`) | `1` = the embedded torrent client runs without DHT. DHT binds the fixed `7246/udp`, so two daemons on one machine fight over it and the second loses its torrent session. Set by `make sandbox`, never needed in production — without DHT a magnet whose trackers are down finds no peers. Any other value leaves DHT on |
| `DISPLAY` | — | `cmd/daemon/main.go` | Linux only. System tray icon shown only when non-empty (graphical session) |

## Service Overrides

These override values from `config.json`. Useful for Docker/CI:

| Variable | Default | Where | Description |
|----------|---------|-------|-------------|
| `NYAA_URL` | `"https://nyaa.si"` | `nyaa/nyaa.go` | Override Nyaa base URL |
| `ANILIST_API_URL` | `"https://graphql.anilist.co"` | `anilist/anilist.go` | Override Anilist GraphQL endpoint |
| `GITHUB_API_URL` | `"https://api.github.com"` | `updates/updates.go` | Override the GitHub API base the update check asks for the latest release (the sandbox points it at `tests/mocks/github`) |

> The former `QBITTORRENT_URL` override of the running daemon was removed along with the external qBittorrent dependency. The name is back only as an entrypoint variable for the first `config.json` (below), with the optional qBittorrent backend.

> `docker/entrypoint.sh` no longer reads a `SAVE_PATH` env var to template the initial `config.json` — the download directory is derived from `COMPLETED_ANIME_PATH` (see `docs/agents/config.md` — Download Path). If `SAVE_PATH` is still set in a Docker Compose file or `.env`, the entrypoint simply ignores it; it no longer appears in the generated config.

## Docker entrypoint (initial `config.json`)

Read **only** by `docker/entrypoint.sh`, which templates the first `config.json` on container
start. They are not read by the Go code, and they do nothing after the file exists unless
`FORCE_CONFIG_UPDATE` is set (which rewrites the whole file — see the caveat below).

| Variable | Default | Config key written | Notes |
|----------|---------|--------------------|-------|
| `COMPLETED_ANIME_PATH` | `/app/downloads/completed` | `completed_anime_path` | The library folder. Point it at a mounted volume — the image only creates `/app/data` |
| `ANILIST_USERNAMES` | `""` | `anilist_usernames` (array) | Comma-separated; `"a, b"` → `["a","b"]`, empty → `[]` |
| `ANILIST_USERNAME` | `""` | — | **Legacy**, accepted as a fallback for `ANILIST_USERNAMES`. Still writes the plural key |
| `MAL_USERNAMES` | `""` | `mal_usernames` (array) | Comma-separated, same conversion as `ANILIST_USERNAMES` |
| `EXCLUDED_LISTS` | `""` | `excluded_lists` (array) | Comma-separated, same conversion |
| `EXCLUDED_LIST` | `""` | — | **Legacy** fallback for `EXCLUDED_LISTS`, same as above |
| `CHECK_INTERVAL` | `10` | `check_interval` | |
| `MAX_EPISODES_PER_ANIME` | `0` | `max_episodes_per_anime` | `0` = no limit, the app's own default |
| `EPISODE_RETRY_LIMIT` | `5` | `episode_retry_limit` | |
| `DELETE_WATCHED_EPISODES` | `true` | `delete_watched_episodes` | `true`/`1` → `true`, anything else → `false` |
| `QBITTORRENT_URL` | `""` | `library_mode`, `torrent_client`, `qbittorrent_url`, `qbittorrent_manage_settings` | Set (the compose file's `qbittorrent` profile passes `AAD_QBITTORRENT_URL`): `"direct"`, `"qbittorrent"`, the URL and `true`. That qBittorrent is the app's alone, so the app manages its settings (decisions/144). Empty writes none of the four: the built-in client |
| `FORCE_CONFIG_UPDATE` | — (unset) | — | When non-empty, overwrites an existing `config.json` |

> The generated file carries **8 of `Config`'s fields** (12 with `QBITTORRENT_URL`); every other
> key comes from the defaults. That is safe on creation —
> `LoadConfigs` unmarshals over `DefaultConfig()`, so the absent fields keep their defaults —
> but `FORCE_CONFIG_UPDATE` rewrites the *whole* file, discarding everything the user set
> through the UI or CLI.

## Docker Compose (`docker/docker-compose.yml`)

Read by Compose, never by the image or the Go code. Set them in `docker/.env` (git-ignored) or the
shell. This is the file for running the app in Docker; `docker-compose.test.yml` is the
integration-test stack with the mocks.

| Variable | Default | Description |
|----------|---------|-------------|
| `AAD_DOCKER_DIR` | `../runtime/docker` (relative to `docker/`) | Host folder holding `data/` (mounted at `/app/data`) and `library/` (mounted at `/library`, the `completed_anime_path`). Both must already exist: the mounts use `create_host_path: false`, so a missing folder fails the start instead of being created owned by root, or on the system disk when a drive is not mounted |
| `AAD_WEB_PORT` | `8091` | Host port of the web UI, bound to `127.0.0.1` only |
| `AAD_UID`, `AAD_GID` | `1000` | User the daemon runs as. It must own both folders (and `qbittorrent/`, with the profile below; qBittorrent runs as the same user) |
| `TZ` | `UTC` | Timezone of the log timestamps |
| `COMPOSE_PROFILES` | — | `qbittorrent` adds the `qbittorrent` service (`qbittorrentofficial/qbittorrent-nox:5.3.0-2`, the version the live test ran against). It mounts `qbittorrent/` (must exist) at `/config` and the library at the **same** `/library`, so no `qbittorrent_path_map` is needed. Without the profile the stack is the daemon alone, as before |
| `AAD_QBIT_PORT` | `8093` | qBittorrent's WebUI port, published on `127.0.0.1` and the same inside the container: qBittorrent answers 401 to a Host header whose port differs from its own. The daemon reaches it as `http://qbittorrent:<port>` on the compose network |
| `AAD_QBITTORRENT_URL` | `""` | Passed to the entrypoint's `QBITTORRENT_URL`, so a first start writes a config on qBittorrent (`http://qbittorrent:8093`). Read only when there is no `config.json` yet; an existing install switches from the Torrent client settings instead |

qBittorrent's first start prints a temporary WebUI password (`docker compose -f
docker/docker-compose.yml logs qbittorrent`). Sign in at `http://localhost:8093` and set a
password of your own, then type it in the app's Torrent client settings. The compose file does not
turn on qBittorrent's "bypass authentication for whitelisted subnets": the host reaches the
published port through the bridge network too, so anything on the machine would get in.

## OS / Path

| Variable | Where | Description |
|----------|-------|-------------|
| `HOME` | `datadir/datadir.go` | Linux/macOS: base for `~/.autoAnimeDownloader/`. `docker/Dockerfile` sets it to `/app/data`, so the state lives in the `/app/data` volume, next to the `config.json` the entrypoint writes there. Before that a root container kept it in `/root` (lost with the container) and any other user got `HOME=/` (not writable) |
| `APPDATA` | `datadir/datadir.go` | Windows: base for `%APPDATA%\.autoAnimeDownloader\` |
| `INVOCATION_ID`, `XDG_CURRENT_DESKTOP`, `XDG_SESSION_TYPE` | `diagnostics/environment.go` | Read only to describe the machine in a bug report (running under systemd, desktop, session). They change no behavior |

## Test-Only

| Variable | Default | Where | Description |
|----------|---------|-------|-------------|
| `DAEMON_URL` | — (unset = skip) | `tests/integration/integration_test.go` | Daemon base URL for integration tests. **Must be set explicitly or every integration test skips** — the suite overwrites the config of whatever daemon answers, so it must never find a developer's daemon by accident. Set to `http://daemon:8091` by `docker-compose.test.yml`. When set but empty/unreachable, tests also skip. The `http://localhost:8091` fallback is only used to build the URL once the gate has passed |
| `TEST_COMPLETED_PATH` | `~/aad-test/library` | `tests/integration/integration_test.go` | `completed_anime_path` the integration tests write. Docker overrides it to `/app/data/aad-test/library` |
| `SCENARIO` | — | `tests/mocks/anilist/`, `tests/mocks/nyaa/` | `"empty"` = mock returns empty data |
| `PORT` | `8080`/`8081`/`8082` | `tests/mocks/*/mock_server.go` | Listen port for each mock server (Anilist, Nyaa, GitHub) |
| `LATEST_TAG` | `v99.0.0` | `tests/mocks/github/` | The release tag the GitHub mock answers |
| `SANDBOX_PORT`, `SANDBOX_ANILIST_PORT`, `SANDBOX_NYAA_PORT`, `SANDBOX_GITHUB_PORT` | `8092`, `18080`, `18081`, `18082` | `scripts/sandbox.sh` | Ports for the `make sandbox` daemon and its three mocks. The default `8092` lets it run next to a real daemon on `8091` |
| `SANDBOX_VERSION` | `0.0.1` | `scripts/sandbox.sh` | Version the sandbox daemon is built as. Not `dev`: a dev build never runs the update check, and the sandbox wants the badge |
| `SANDBOX_SKIP_FRONTEND` | — | `scripts/sandbox.sh` | `1` = don't rebuild the frontend, embed the last `dist/` |
| `TEST_LOGGER_INIT` | — | `logger/logger_test.go` | When empty, auto-inits logger for tests |
