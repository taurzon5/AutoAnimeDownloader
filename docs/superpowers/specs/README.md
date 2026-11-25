# Specs

Only specs that have **not** been executed live here. Implemented ones were deleted; they
remain in git history. The decisions that came out of them live in
[decisions/](../../agents/decisions/index.md).

| Spec | Status |
|------|--------|
| [source-as-descriptor](2026-09-13-source-as-descriptor-design.md) | Designed, not executed — `v3.0.0` ([TODO](../../TODO.md), [checklist](../../v3.0.0.md)) |
| [subtitle-language-filter](2026-08-24-subtitle-language-filter-design.md) | Designed, not implemented — no fixed release ([backlog](../../backlog/subtitle-language-filter.md)) |
| [dashboard-recent-shelf](2026-10-23-dashboard-recent-shelf-design.md) | Designed, not implemented — `v2.12.0` ([TODO](../../TODO.md)) |
| [direct-library-mode](2026-11-22-direct-library-mode-design.md) | PR 1 of 4 implemented (direct mode, one root); PRs 2–4 not started — no fixed release |
| [qbittorrent-backend](2026-11-23-qbittorrent-backend-design.md) | Steps 1 (the backend) and 2 (queue and limits) implemented on `feat/qbittorrent-backend`, not merged; step 3 (import) not scheduled. Questions answered — qBittorrent becomes the only client (rain is being dropped): the Web API backend, placement through renames; 5.0+ only; an import from rain designed but not a priority |
| [megapack-downloads](2026-11-23-megapack-downloads-design.md) | Designed, decisions recorded — torrents holding a whole franchise: franchise groups, mapping files to AniList entries, file selection through qBittorrent (rain is being dropped), megapack-first search order, a megapack queue; waits for the qBittorrent backend |
| [shoko-integration](2026-11-23-shoko-integration-design.md) | Step 1 (matching and the sync), full megapacks and the drop folder implemented on `feat/shoko-sync`, not merged; steps 2–3 wait for the release filters — read a Shoko Server library: episodes already in Shoko are not downloaded, their files are judged by the release filters, upgrades listed but not downloaded by default |
| [release-filters](2026-11-23-release-filters-design.md) | Designed, decisions recorded — release filters (autobrr-style) on app-made lists, a daily Nyaa sweep and a scheduled search queue, upgrades with a Replaced page, and the anime page's downloaded-torrents section; 6 steps, none started |

