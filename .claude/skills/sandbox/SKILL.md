---
name: sandbox
description: Run AutoAnimeDownloader's real web UI on top of the real daemon, isolated from the user's installation, and check it in headless Firefox. Use when asked to see, open, click through, screenshot or "confirm it works" in the running app, to dogfood/QA the UI, or after a frontend or API change that tests alone don't show. Never start the daemon any other way for this.
---

# Sandbox: the real app, without the user's data

`make sandbox` (`scripts/sandbox.sh`) starts a throwaway daemon on `http://localhost:8092` with a
scratch data dir (`.sandbox/home`, wiped on every start), mock AniList/Nyaa and no tray or desktop
browser. What it isolates and why: [commands.md](../../../docs/agents/commands.md#running-the-daemon).

**Never** run `go run ./src/cmd/daemon`, the built daemon, `make dev` or `autoanimedownloader start`
to look at the app: they use the user's real `~/.autoAnimeDownloader` (config, state, torrents) and
the real AniList/Nyaa, and port 8091 may be the user's own running daemon.

## 1. Start it (background)

Run the script directly, not through `make`, so it can be stopped cleanly. `$SCRATCH` below is
your session's scratchpad directory — write its absolute path, it is not an env var:

```bash
bash scripts/sandbox.sh > "$SCRATCH/sandbox.out" 2>&1        # run_in_background: true
```

Then wait for it in one call — success or failure, never just one of them:

```bash
for i in $(seq 1 240); do grep -q -E 'Sandbox up|already in use|exited during startup|error' "$SCRATCH/sandbox.out" && break; sleep 0.5; done; tail -8 "$SCRATCH/sandbox.out"
```

The first start builds the frontend (~30s). If only Go code changed since the last run,
`SANDBOX_SKIP_FRONTEND=1` reuses the last build. `port ... already in use` → another sandbox is
still up (stop it, step 4) or set `SANDBOX_PORT`.

## 2. Drive it with Playwright on Firefox

Firefox only: never Chrome, Chromium, or a browser agent that runs on them
([decisions/157](../../../docs/agents/decisions/157-the-smoke-tests-run-on-firefox-only-never-on.md)).
The driver is the Playwright the smoke tests use, with its own Firefox build
(`bunx playwright install firefox` in `src/internal/frontend/` once, and again after a Playwright
bump). Write a short script to `$SCRATCH/look.mjs`, with `<checkout>` the absolute path of the
checkout you work in and `<scratchpad>` your scratchpad's:

```js
import { firefox } from '<checkout>/src/internal/frontend/node_modules/@playwright/test/index.mjs'

const SCRATCH = '<scratchpad>'
const browser = await firefox.launch()                   // headless
const context = await browser.newContext({ viewport: { width: 375, height: 812 }, colorScheme: 'light' })
const page = await context.newPage()
const errors = []                                        // JS errors are findings too
page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()) })
page.on('pageerror', (e) => errors.push(String(e)))

await page.goto('http://localhost:8092/#/status', { waitUntil: 'networkidle' })
console.log(await page.locator('main').ariaSnapshot())   // roles and names: what you can click
await page.getByRole('button', { name: /^check now$/i }).first().click()  // the header's: the first-steps card has one too until a check has run
await page.screenshot({ path: `${SCRATCH}/status.png` })  // your own look: Read it. Evidence goes in step 5
console.log('errors:', errors)
await browser.close()
```

```bash
node "$SCRATCH/look.mjs"
```

One run is one look: edit the steps and run it again. Each run starts a fresh browser, so its own
state (localStorage, a dismissed card, a picked theme) doesn't carry over; the daemon's does. Act
by role, label and name (`getByRole`, `getByLabel`), as the smoke specs do, and fall back to
`page.locator('<css>')` only when nothing has a name.

Routes are hash-based (table in `src/internal/frontend/src/App.svelte`): `#/status`, `#/add`,
`#/downloads`, `#/config`, `#/priorities`, `#/notifications`, `#/logs`, `#/report`, and
`#/status/<id>` for an anime's detail page. The UI is English only; for width/layout checks look at
the narrowest widths, 320px included (see [testing.md](../../../docs/agents/testing.md#layout-regressions-testssmokelayoutspects)).

The API and CLI hit the same daemon: `curl -s localhost:8092/api/v1/status`,
`AAD_API_ENDPOINT=http://localhost:8092 go run ./src/cmd/cli <command>`.

## 3. Know what the data can't show

The AniList mock answers every query with one anime ("Test Anime", no media id, no released
episodes) and the Nyaa mock is never searched. Good for layout, navigation, config, priorities,
the add-anime flow, logs and the WebSocket. **Not** good for anything that needs a download, a
torrent, episode rows or a real anime detail page — say so in the report instead of claiming the
flow was checked. Daemon logs: `.sandbox/daemon.out`.

## 4. Stop it — always, also after a failure

```bash
kill "$(cat .sandbox/sandbox.pid)"
sleep 2; for p in 8092 18080 18081; do (exec 3<>/dev/tcp/127.0.0.1/$p) 2>/dev/null && echo "$p still open"; done
```

SIGTERM runs the script's trap, which stops the daemon and both mocks and removes the pid file.
Not `kill -INT`: a script started in the background inherits SIGINT as ignored, so nothing happens.
Not `pkill -f scripts/sandbox.sh`: the pattern also matches the shell running the `pkill` and kills it. Anything still open → find it
with `pgrep -af '\.sandbox/bin'` and kill it. The look script closes its own browser; a run that
crashed before `browser.close()` can leave one, which `pgrep -af 'ms-playwright/firefox'` finds.

## 5. Report — with evidence the developer can open

The terminal shows no images, so every problem gets a file the developer can click instead of
starting the app to see it. Save them in one folder per run — `.sandbox/reports/` is gitignored
and survives the sandbox restarts (the script wipes everything else in `.sandbox/`):

```bash
REPORT="$PWD/.sandbox/reports/$(date +%Y-%m-%d_%H%M)"; mkdir -p "$REPORT"; echo "$REPORT"
```

Then, in the look script, with that path as `REPORT`:

```js
await page.locator('<selector of the broken element>').screenshot({ path: `${REPORT}/downloads-375-chip-wraps-before.png` })
await page.screenshot({ path: `${REPORT}/downloads-375-page.png`, fullPage: true })   // when the context matters
```

- **One screenshot per problem**, named `<screen>-<width>-<what>-before.png`. Prefer the element
  (`locator(...).screenshot`) — a full page at 375px is a 5000px strip nobody can read; add the
  page only when the problem is where the element sits.
- **Video only for behavior** a still can't show — a click that does nothing, a menu that closes,
  something that flickers. Keep it to the repro, a few seconds: add
  `` recordVideo: { dir: `${REPORT}/video`, size: <the viewport> } `` to `newContext`, do the
  steps, then `await context.close()` and
  `` await page.video().saveAs(`${REPORT}/<screen>-<what>.webm`) `` before `browser.close()`.
- **Fixed it? Add the after shot.** Same route, width, theme and selector as the before,
  so the two compare one to one; name them as a pair: `downloads-375-chip-wraps-before.png` /
  `-after.png` (same for a video). The frontend is embedded in the daemon binary, so restart the
  sandbox **without** `SANDBOX_SKIP_FRONTEND` first — otherwise the after shot shows the old code
  and the fix looks like it failed. A problem only reported, not fixed, has just the before.
- Your exploratory screenshots stay in the scratchpad; only evidence of a problem goes in the report.

Then tell the user what was checked, what was not (step 3), and for each problem: route, width and
theme, what is wrong, the steps, whether it was fixed, and the **full absolute path**
of each file (before and after) — a relative path isn't clickable in the terminal.
