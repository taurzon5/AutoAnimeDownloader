# 157. The smoke tests and the sandbox's browser checks run on Firefox only, never on Chrome or Chromium

**Location:** `src/internal/frontend/playwright.config.ts` (the one `firefox` project, with
`devices['Desktop Firefox']`), the "Install Playwright browsers" step of the smoke job in
`.github/workflows/build.yml` (`playwright install --with-deps firefox`), the setup line in
[testing.md](../testing.md), and step 2 of the sandbox skill
(`.claude/skills/sandbox/SKILL.md`: a short Playwright script that launches `firefox`).

**What it looks like:** Playwright's default browser, its docs and most examples use Chromium, and
the suite ran on Chromium until 2026-11-26. Adding a `chromium` project back looks like free
coverage, and `channel: 'chrome'` looks like a way to skip a download when Chrome is installed.
The sandbox skill writing a throwaway script, where a browser agent CLI would take one command per
step, looks like extra work.

**Why it's right:** on 2026-11-26 the user said they never want to run Chrome or its engine. The
suite runs on the user's machine, so a Chromium project would launch Chromium there every time
someone runs `bun run test:smoke`. The CI job uses the same config, so it runs Firefox too, and
what passes in CI is what runs locally. The layout width checks (`tests/smoke/layout.spec.ts`)
were written against Chromium's text widths; they hold on Firefox at the same 1280px default
viewport, but a new width check is measured on Firefox (as
[#39](039-a-finite-choice-strip-wraps-flex-wrap-an-unbounded.md)'s Status row was, again).

The sandbox skill used to drive the app with `agent-browser`, a browser agent that runs Chromium.
It now uses the Playwright and the Firefox build the smoke tests already need, so a browser check
installs nothing new. Everything the old step did works there: the structure of the page
(`ariaSnapshot`), clicks by role and name, page and element screenshots, video, console errors.

Playwright drives its own patched Firefox build, not the system `/usr/bin/firefox`, and each
Playwright version expects one build revision (`node_modules/playwright-core/browsers.json`). After
a Playwright bump, run `bunx playwright install firefox` again.

**Don't "fix" by:**
- Adding a `chromium` or `webkit` project "for coverage": it launches the engine the user refuses
  on their machine.
- `channel: 'chrome'` or `channel: 'msedge'`: that is the installed Chrome or Edge, the same engine.
- Pointing `executablePath` at the system Firefox: Playwright can't drive a stock Firefox.
- Putting `agent-browser`, or another browser agent, back into the sandbox skill: check which
  engine it launches first; the common ones run Chromium.
- Looking at the app in the Claude desktop app's built-in browser pane: it is Electron, so
  Chromium.
