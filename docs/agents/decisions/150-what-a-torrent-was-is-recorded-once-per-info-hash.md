# 150. What a torrent was is recorded once per info hash, at add time, in releases.json, and the daemon holds that store as package state

**Location:**
- `internal/files/releases.go`: `ReleaseStore` (`releases.json` in the data dir) and
  `ReleaseRecord`; `Record` keeps the first record of a hash and only adds episodes.
- `internal/daemon/releases.go`: `releaseStore` (set by `SetReleaseStore` in `cmd/daemon/main.go`),
  `recordRelease` and `recordMagnet`, called by every add path: `processAnimeEpisodes` (from the
  candidate `attemptDownloadWithRetries` now returns), `ManualDownloadEpisode`,
  `ManualDownloadEpisodeWithMagnet`, `ManualDownloadAnimeWithMagnet` and
  `DownloadStandaloneAnimeWithMagnet`.
- `internal/nyaa/release.go`: `ParseRelease`, run once at add time.
- `internal/api/endpoint_anime_releases.go`: `animeReleaseCards`, for
  `GET /animes/{id}/releases`; config `keep_removed_release_cards`.

Pinned by:
- `TestReleaseStoreRecordKeepsTheFirstRecord` and `TestReleaseStoreNil`.
- `TestProcessAnimeEpisodesRecordsTheRelease`.
- `TestAnimeReleaseCards` and `TestHandleAnimeReleases`.
- `TestParseRelease`, `TestExtractorFixes` and `TestWithRowInfo`.
- `TestPickedReleaseRecordsAPickOrAPaste`, and `TorrentPicker.test.ts` for the picker's fields.

**What it looks like:**
- A file of its own beside `episodes.json`, although every episode record already carries the hash.
- A package-level store in `daemon` instead of a field of `State`.
- Adding the same torrent again doesn't update its record.
- A card for a torrent that is gone is hidden when `keep_removed_release_cards` is off, but its
  record stays in the file.

**Why it's right:**

- **One torrent, many episodes.** A pack is one torrent behind many episode records. The release
  name, the Nyaa page, the seeders when chosen and the parsed tags belong to the torrent. Copied into
  every episode record, they would go stale one copy at a time. Keyed by info hash, the anime page
  joins them to `episodes.json` by `EpisodeHash` (the release-filters spec §9).
- **Written at add time, from the candidate.** The Nyaa row exists only during the search: after
  the add, the client knows only the torrent's name and size. So `attemptDownloadWithRetries`
  returns the candidate it added, not just its size, and each add path records it. A magnet has no
  row, so its name is the magnet's `dn`.
- **The first record wins.** The pass adds a pack once per episode it covers, and a redownload
  re-adds the same torrent ([#140](140-redownloading-missing-files-adds-the-same-torrent.md)). Both
  are the same torrent: overwriting would replace "found by a check, 40 seeders" with whatever the
  later add knew. Only the episodes join, so the record lists every episode added from it. That
  list is what a removed torrent's card still shows once `episodes.json` has forgotten it.
- **Package state, not `State`.** `attemptDownloadWithRetries` sits deep in the pass, and the manual
  downloads are called from API handlers. None of them carries `State`, and threading it through
  every caller and test would be a large diff for one write. Unset (tests, `debug-anime`, which adds
  nothing), recording does nothing. The API reads the same value through `daemon.ReleaseStore()`.
- **A failed write is logged, not returned.** The torrent is already in the client, and refusing
  the add because the history file is unwritable would lose an episode over a display detail. The
  card then falls back to the client's name.
- **Torrents from before the store** have no record. Their card takes the client's name and size,
  parses the tags from that name, and says "added before release details were kept".
- **Hiding, not deleting, removed cards.** `keep_removed_release_cards` (on by default, the user's
  decision 6 in the spec) decides whether the anime page shows a torrent that is gone. Off hides
  them; the records stay, so turning it back on brings the history back. One record per torrent
  ever added is a small file. It lives in `config.json`, unlike the first-steps card's dismissal
  ([#63](063-dismissing-the-first-steps-card-lives-in.md)): the API filters the cards, so the
  CLI's `animes releases` shows what the page shows, and the user asked for a setting.
- **Which extractor fixes reach the ranking.** The spec planned three for step 1, and they are in
  the extractors the ranking shares. A `(Group)` name now has a group, and so does a scene name's
  `-GROUP`, but the ranking matches groups by substring and never reads them. `Opus` is read, last,
  so a name with another codec still ranks by it, and an Opus-only name ranks as before (not in the
  list either way). `AVC` reads as H.264, and this one **can move a pick**: an Erai-raws name now
  carries a codec, so it no longer ties on `codec`
  ([#69](069-codec-now-applies-to-episodes-and-a-name-with-no.md)). With the default list (`h.264`
  first) it now beats a tagged HEVC release where `fansub` used to decide; with an HEVC-first list,
  the reverse. That is the fix the spec asked for. The golden set's six cases pick the same
  torrents, which shows no case regressed, not that nothing moves.
  **Amendment (2026-11-26):** the extractors read underscores as spaces (`underscoresAsSpaces`), so
  an underscore-joined name (`(Hi10)_Gosick_-_01_(BD_720p_AAC)`) now carries its resolution, source,
  codec and audio, and so do `ParseRelease`'s own tags; the group keeps the raw name. This **can move
  a pick** the same way: such a name no longer ranks last on resolution, and a `_BD_` name of a
  finished series ranks as BD instead of the assumed WEB-DL. The golden set still picks the same
  torrents, which again shows no regression, not that nothing moves.
- **What stayed out of the ranking, then came in.** At step 1, E-AC-3 and plain DTS were read only
  by `ParseRelease`. On 2026-11-27 the ranking read them too (amendment below).
- **A pick or a paste.** The torrent picker and a pasted magnet reach the same add paths (the
  anime replace, the standalone add). A request that carries the picker's Nyaa id is recorded as a
  pick, with its Nyaa page and size (`PickedRelease`). Without one it is a pasted magnet named by
  its `dn`.

**Amendment (2026-11-26):** `POST /animes/{id}/episodes/{n}/replace` takes the picker's row too (`ManualDownloadEpisodeWithMagnet` with a `PickedRelease`), so an episode picked from the single-episode picker is recorded as a pick; a pasted magnet still records its dn.

**Amendment (2026-11-27):** the ranking reads E-AC-3 as Dolby Digital Plus (`EAC3`, `E-AC3`,
`E-AC-3` → `DDP`, so above AAC) and a plain `DTS`, but only when the name names no other codec: the
DTS pattern is the last but Opus, so "DTS + AAC" still ranks as AAC. `dts` joins the default audio
list after `ddp`; a saved list without it ranks a DTS-only name last, as before, when it read as
nothing. The audio criterion only sorts movies, and the golden set picks the same torrents.
`ParseRelease` keeps its own list (a DTS track is DTS there whatever else the name says).

**Don't "fix" by:**
- Moving the release details into `episodes.json`.
- Updating a record when the same hash is added again.
- Failing an add because the record could not be written.
- Recording from the client's torrent name after the add: the Nyaa row is gone by then.
- Moving the DTS pattern ahead of AAC to match `ParseRelease`: "DTS + AAC" names would rank as DTS.
- Deleting records when `keep_removed_release_cards` is off.
