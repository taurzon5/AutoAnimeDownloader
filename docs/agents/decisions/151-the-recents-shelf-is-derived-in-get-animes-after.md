# 151. The Recents shelf is derived in GET /animes after the list merge, and only watching takes an episode off it

**Location:**
- `internal/api/recent_shelf.go`: `recentShelf`, called by `handleAnimes` for every anime after the
  AniList/MAL/standalone merge.
- `internal/files/filemanager.go`: `Config.RecentShelfDays` (`recent_shelf_days`, default 3, 0..30,
  checked in `endpoint_config.go`).
- `frontend/src/lib/domain/recent.ts`: `recentShelf` (sort and the cap of 12) and `recentCaption`;
  the shelf and the linked last-check rows in `routes/Status.svelte`.
- `frontend/src/lib/utils/status.ts` and its CLI copy `cmd/cli/animes_filter.go`: `scopeToAccounts`
  takes `recent` from the selected account furthest behind.

Pinned by:
- `TestRecentShelf`, `TestHandleAnimesCarriesRecent`, `TestRecentShelfDaysConfig` and
  `TestRecentShelfDaysDefaultsToThree`.
- The frontend's `recent.test.ts` and `Status.recent.test.ts`; the CLI's
  `TestAnimesAccountFilterProjectsProgress` (`animes --account`).

**What it looks like:**
- No endpoint of its own and no "seen" state: opening a card doesn't take it off the shelf.
- The server attaches `recent` to every qualifying anime; the frontend keeps 12.
- A pack whose watched part shrinks shows only its unwatched remainder, not its declared range.

**Why it's right:**

- **After the merge, from the saved episodes.** An episode qualifies when it is numbered above the
  watch progress and was downloaded inside the window. The progress is only known after the list
  merge (AniList, MAL, or a standalone's own `AnimeSettings.Progress`), and `GET /animes` already
  walks every saved episode, so a second pass over that slice costs no I/O. A separate endpoint
  would repeat the merge.
- **Watching is the dismissal.** The shelf answers "what landed that I haven't watched". An
  "opened" flag would hide an episode the user only looked at, and would be state to store and
  sync across tabs. AniList progress on this endpoint can lag by its 60 s cache plus the 30 s poll,
  the same lag the Unwatched filter has.
- **Whose watching.** With no user filter, the combined progress: the slowest account
  ([#43](043-an-animes-identity-is-mediaid-status-is-a-per.md)), as for the table. Each account
  also carries its own `recent` (`accounts[].recent`), and the dashboard's user filter (and the
  CLI's `animes --account`) projects the shelf onto the picked account furthest behind
  (`scopeToAccounts`), as it projects the progress
  ([#125](125-the-dashboard-user-filter-re-projects-progress.md)). Built only from the combined
  progress, picking alice would show episodes alice watched because bob hasn't: the symptom #125
  calls a bug. The search and type filters don't touch the shelf; the user filter is whose shelf it
  is.
- **AniList degraded, accepted.** When the list fetch fails or comes from the cache, the orphan
  refresh is skipped ([#65](065-anilist-budget-is-spent-by-the-orphan-refresh-not.md)): an orphan keeps
  no progress, and a cached list's progress can be hours old. Episodes already watched can then
  come back onto the shelf until AniList answers again. The Unwatched filter has the same lag;
  hiding the shelf in that state would hide fresh downloads too.
- **A pack is only one run from one torrent.** `is_batch` needs two or more qualifying episodes on
  one hash, the same reading the anime page uses for a pack. A single leftover of a pack reads as
  "Episode N". The caption uses the unwatched remainder, so a 1–12 pack watched to 5 says 6–12.
  Several runs read as episodes even from one torrent: "1–3, 5 · pack" would claim a pack with a
  hole.
- **No cap on the server.** Every qualifying anime carries `recent`, so the table and any later
  consumer can use it; 12 is a frontend constant, not a setting.
- **Empty means absent.** No shelf without cards, like the last-check report: a permanent empty
  block trains the eye to skip it. After an upgrade the shelf starts empty; nothing is backfilled.

**Don't "fix" by:**
- Adding a dismiss button, or dismissing on open.
- Computing `recent` before the merge: the progress isn't known yet.
- Feeding the shelf from the unprojected list under the user filter, or dropping `accounts[].recent`.
- Capping on the server.
- Captioning a pack from its declared `BatchStart`/`BatchEnd`.
