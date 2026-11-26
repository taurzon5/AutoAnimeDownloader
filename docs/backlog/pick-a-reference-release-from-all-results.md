# Pick a reference release from all of an anime's Nyaa results

Found on 2026-11-25 while trying the torrent picker on an airing anime in the test instance. Not
started. Delete this file when it is fixed or dropped.

## What the user asked

A section that fetches **all** of an anime's Nyaa results, packs and single episodes, and lets the
user select one as the **reference**: the app then downloads the anime's other episodes from the
same release group and in the same quality. Not tied to a release yet.

## What the screen does today

The anime page's **Choose torrent** screen (`src/internal/frontend/src/routes/TorrentPicker.svelte`,
`GET /api/v1/animes/{id}/torrent-candidates`) lists packs only. `PreviewWholeTitleTorrents`
(`src/internal/daemon/preview.go`) keeps the packs from `partitionSearchResults`, drops every
single episode, and stops at `maxTorrentCandidates` (8). The screen says so: "single episodes stay
off this screen".

Measured on 2026-11-25 with The World's Strongest Witch (AniList 211778, airing, episode 8 out):
Nyaa returns 75 results for `strongest witch`, one full page, all single episodes. They come from
ToonsHub, VARYG, AnoZu, Judas, Yameii, Doomdos and Tsundere-Raws, in 720p and 1080p, CR and
Bilibili WEB-DL, H.264 and HEVC, Japanese, dual audio and English dub. No pack exists yet, so the
screen shows "0 packs" and "No packs, try another query" with nothing to choose. For an airing
anime that is the normal case until its season ends.

## Overlap with the release-filters spec

[Release filters](../superpowers/specs/2026-11-23-release-filters-design.md) §6 "Following a
release" (delivery step 4) designs the follow part: a release downloaded by hand from the picker
can be followed, and a later release is the same line when it shares the group and the key tags
(resolution, source and service, audio languages), with a per-tag override. §5 groups the picker's
candidates by the filter they match. What this note adds:

- the picker lists single episodes too, so an airing anime has something to pick;
- the pick may be a reference only, followed without downloading that torrent (pick episode 8's
  release to steer episode 9 on). To decide.

When step 4 is designed, fold this in and delete the file.

## What to check

- **All results, grouped.** Packs first as today, then single episodes grouped by release line
  (group plus key tags), one row per line with the episodes it has (`EP 1–8`), so 75 rows read as
  about ten lines. Step 1's release parsing (`nyaa.Release`) reads the group and the tags. Check
  that scene-style names (`… H.264-VARYG`) get their group, a bug the spec lists.
- **The search.** A typed query drops season and part (`ScrapNyaaForAnime(used, nil, nil, nil)`).
  Single episodes need the episode parsing the pass uses, and the page limit matters: 75 results
  already fill one Nyaa page. `maxTorrentCandidates` would cut the list.
- **A missing episode.** When the reference line hasn't brought an episode: wait, or fall back to
  the anime's normal pick. The spec proposes handing over 7 days after airing.
- **Where it is stored.** Per anime, next to `AnimeSettings`, shown on the anime page with "stop
  following". Its API action ships with its CLI command in the same change
  ([conventions.md](../agents/conventions.md)).
- **The empty state.** Until then, "No packs" on an airing anime could say why, for example "No
  packs yet: 75 single episodes found, it is still airing", instead of "try another query".
