# The fork still sends its users to the upstream repo

Found on 2026-11-25 when the user said the fork no longer aims to go back to upstream
(icarosuper/AutoAnimeDownloader). Not started. Delete this file when it is fixed or dropped.

## Symptom

The fork is its own project now, but the app and its docs still point at icarosuper's repo:

- **Update check:** `src/internal/updates/updates.go` (`releasePath`) reads icarosuper's latest
  release, so the app offers upstream's version as an update. Following it replaces the fork with
  upstream. This one matters most.
- **Bug reports:** `REPO_URL` in `src/internal/frontend/src/lib/report.ts` opens the issue on
  icarosuper's repo, and `CONTRIBUTING.md` links upstream's bug and feature request forms.
- **Install:** `install.sh` (`REPO`), the README's install steps and release links, and
  `docs/distribution.md` (the ghcr image, the install command) install upstream's releases.
- **README:** the CI badge and the clone URL; `docs/guides/development.md` clones upstream too.
- **Releases:** `.claude/skills/release/SKILL.md` builds its checksum URLs from icarosuper's
  releases, and its last step bumps the AUR package `autoanimedownloader-bin`. Check who maintains
  that package: if it is upstream's, the fork needs its own package name or no AUR step.
- **The rename:** [v3.0.0](../v3.0.0.md) open decisions D1 (repo slug), D2 (Go module path) and D8
  (ghcr image) assume `icarosuper`.

## What to check

- Point each one at `taurzon5/AutoAnimeDownloader` (or the v3.0.0 name, if the rename lands first:
  then do it once, as part of that release). The update check's path, its test
  (`src/internal/updates/updates_test.go`), the GitHub mock
  (`src/tests/mocks/github/mock_server.go`), the `example` tag of `LatestVersionURL` in
  `src/internal/api/endpoint_status.go`, and `docs/agents/architecture.md`'s update-check paragraph
  change together.
- The fork has no releases yet, so the update check would find none. That is harmless: a non-200
  answer is logged at debug level and tried again the next day.
- **What stays:** the README's "This is a fork of icarosuper/AutoAnimeDownloader" line, which
  credits the original. `src/tests/unit/anilist_test.go` uses `icarosuper` as an AniList username
  against the mock, which isn't a link to the repo.
- The `upstream` git remote can stay for reading; nothing pushes to it.
