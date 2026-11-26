# A browser inside the app, preferably Firefox with uBlock Origin

Found on 2026-11-25 while trying the torrent picker on an airing anime in the test instance. Not
started. Delete this file when it is fixed or dropped.

## What the user asked

A browser reached from the app's web UI, to browse sites such as nyaa.si without leaving the app.
Preferably Firefox with uBlock Origin. Not tied to a release yet.

Added later the same day: the app **gathers every title and link on the pages the user opens in
that browser**, so what was browsed can be sent to the app: to an anime, or to the manga and novel
section ([manga-and-novel-downloads.md](manga-and-novel-downloads.md)).

## What to check

- **Where it runs.** A web page can't host a real Firefox. The usual way is a container that runs
  Firefox and streams it to the user's browser (`jlesage/firefox` over noVNC, `linuxserver/firefox`
  over KasmVNC or Selkies), shown in a page of the app or opened from a link. That fits the Docker
  install. The desktop install (tray, `autoanimedownloader start`) runs on a machine that already
  has a browser. Decide whether it is Docker only (a compose service next to the app) or something
  the app starts itself.
- **uBlock Origin** can be installed, and kept updated, through Firefox's enterprise policies: a
  `policies.json` whose `ExtensionSettings` points at the addons.mozilla.org listing with
  `installation_mode: force_installed`. Check that the chosen image lets you mount one.
- **A plain iframe of nyaa.si** works without a container: on 2026-11-25 nyaa.si sent no
  `X-Frame-Options`, and its CSP only had `upgrade-insecure-requests`. But nothing blocks ads
  there, and a magnet link opens the user's own torrent handler, not the app.
- **Getting a pick into the app.** Browsing is half of it: a release chosen there has to reach the
  app. Today the picker has "Or paste a magnet". Options: copy and paste, a magnet handler set up
  in that Firefox that sends to the app's API, or the reference pick in
  [pick-a-reference-release-from-all-results.md](pick-a-reference-release-from-all-results.md).
- **Gathering titles and links.** A page served over HTTPS can't be read on the way, so the browser
  itself has to hand the page over: a WebExtension installed by the same `policies.json` as uBlock
  Origin, whose content script reads each page's release titles and magnet or `.torrent` links and
  posts them to the app's API. Decide which sites it reads (an allowlist starting with nyaa.si, or
  every page), where the gathered list shows in the app, and how long it is kept. The receiving
  endpoint ships with its CLI command.
- **Security.** That browser runs code from any site and keeps its own cookies. Keep its port on
  127.0.0.1 like the app's, and give it no app credentials. Measure its RAM while open.
- **CLI parity.** A page that only opens the browser has no API action. If it gets an endpoint
  (start or stop the container), that endpoint ships with its CLI command
  ([conventions.md](../agents/conventions.md)).
