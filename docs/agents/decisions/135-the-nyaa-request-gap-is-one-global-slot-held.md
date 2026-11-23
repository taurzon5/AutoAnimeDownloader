# 135. The Nyaa request gap is one global slot held across the sleep, not a pause per search

**Location:** `nyaa/pacing.go` — `waitForRequestSlot`, `SetRequestDelay`/`ActiveRequestDelay`,
`paceMu`/`lastRequest`; the call at the top of `fetchNyaaPage` (`nyaa/nyaa.go`);
`files/filemanager.go` — `Config.NyaaRequestDelaySeconds`, `ApplyNyaaSettings`. Pinned by
`TestWaitForRequestSlot_ConcurrentRequestsAreSpaced`,
`TestWaitForRequestSlot_SecondRequestWaitsTheRestOfTheGap`,
`TestWaitForRequestSlot_NoWaitOnceTheGapHasPassed` and `TestFetchNyaaPage_WaitsBetweenRequests`
(`nyaa/pacing_test.go`).

**What it looks like:** a package-level mutex that is held while the goroutine sleeps, so every
other Nyaa request in the process queues behind it. It reads like a lock held too long, and
like a delay that belongs inside `fetchSearchPages` between one page and the next.

**Why it's right:** `nyaa_request_delay_seconds` exists so the daemon can stay under nyaa.si's
429/Cloudflare threshold, and that threshold counts requests from the machine, not per search. The
pass searches up to `maxConcurrentAnimes` animes at once (`daemon/verification.go`), and a
search also fetches torrent detail pages. A pause inside each search would still send the first
page of every concurrent search together, which is the burst the setting is meant to stop.
Holding `paceMu` across the sleep is what turns N requests made at once into N requests spaced
one gap apart: each one waits for the slot, sleeps only what is left of the gap since the
previous request, and claims the slot. The first request of the process, or one made after the
gap has passed, goes out at once. Hooking `fetchNyaaPage` covers search pages and detail pages
alike, because every Nyaa HTML request goes through it
([#57](057-adaptive-pagination-sequential-with-a-candidate.md) (d)).

The default is `0` (off), and with the delay off `waitForRequestSlot` returns before touching the
lock, so the default path costs nothing and the golden set replays unchanged. Two trade-offs are
accepted. With a gap set, a pass takes about `gap × requests` longer; that cost is the reason the
user chose the gap. And requests the user triggers from the web UI or CLI (the Add picker's
search, a candidate's detail page `GET …/torrent-candidates/{nyaaId}`, a manual download's
search) take the same slot: during a pass they wait behind every request already queued, which
with 5 concurrent searches and a 5 s gap is tens of seconds. That is on purpose, because
nyaa.si counts those requests from the same machine too.

**Don't "fix" by:** releasing the lock before sleeping (concurrent waiters would compute the same
wait and wake together, which is the burst again); moving the wait into `fetchSearchPages` or a
per-search counter (concurrent searches and detail fetches would bypass it); exempting API- or
UI-triggered fetches from the slot "so the picker feels fast" (a click during a pass would burst
alongside the pass, which is what the gap prevents); waiting before the first request too (it adds
a full gap to every pass for no traffic saved).
