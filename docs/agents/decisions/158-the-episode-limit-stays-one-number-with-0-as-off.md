# 158. The episode limit stays one number with 0 as off, drawn as a switch, and is off by default

**Location:** `internal/files/filemanager.go` — `DefaultConfig` (`MaxEpisodesPerAnime: 0`) and the
`MaxEpisodesPerAnime` field; `frontend/src/routes/Config.svelte` — `episodeLimitOn`,
`toggleEpisodeLimit`, `episodeLimitLifted` and the "Limit Episodes per Anime" switch in the
Downloads group; `docker/entrypoint.sh` (`MAX_EPISODES_PER_ANIME` defaults to `0` in a first
`config.json`, 2026-11-27); `internal/daemon/golden_test.go` — `runGoldenPass` pins `12`. Pinned by
`tests/component/Config.episodeLimit.test.ts`.

**What it looks like:** Delete Watched Episodes Automatically (in Library's Cleanup block since
2026-11-26) is a real bool
(`delete_watched_episodes`) with an amount (`watched_episodes_to_keep`). The episode limit is one
int where `0` means off, and the page builds the switch out of it. The obvious cleanup is a
`limit_episodes` bool so the two match. And off by default looks less safe than the old 12.

**Why it's right:** on 2026-11-25 the user asked for the limit to be something they can turn off,
off by default. `0` already meant "no ceiling and no end to the pack window" in the daemon
(`effectiveMax`, `windowEnd`), so keeping it as the off value means `config set
max_episodes_per_anime 0` and every existing `config.json` keep working with no migration
([#90](090-there-is-no-configjson-versioning-the-migration.md)). A separate bool would need one,
and would leave two fields that can disagree (bool on, amount 0).

Off by default changes new installs only: `LoadConfigs` unmarshals over the defaults, so a saved
`12` stays `12`. With no limit, every aired episode of a list entry downloads and a pack search
covers all of them in one pass. The brakes left are `max_batch_torrent_size_gb` and the disk guard
(`checkRootBudgetSpace`); the limit never bounded packs anyway
([#48](048-max-episodes-per-anime-never-limited-batches-the.md),
[#61](061-a-pack-episode-is-immune-to-the-limit-prune-and-to.md)). In direct mode with keep forever
the daemon ignores the limit already
([#137](137-direct-mode-deletes-through-the-torrent-and-keep.md)), so there the switch is greyed
out with a line saying why.

The switch keeps its own state (`episodeLimitOn`), set when the config loads, instead of reading
`!== 0` each time: typing `0` or clearing the amount then leaves the field on screen with its error
and blocks Save, rather than hiding the field as if the user had turned it off.

The golden set runs on `DefaultConfig()` and was recorded with the old default. With the window
open its picks would move, so `runGoldenPass` pins `12`.

**Don't "fix" by:**
- Adding a `limit_episodes` bool: it breaks `config set max_episodes_per_anime 0` and saved
  configs, and creates a state where the bool and the amount disagree.
- Putting the default back to 12 "to be safe": the user asked for off by default.
- Deriving the switch from `max_episodes_per_anime !== 0` again: typing `0` hides the field.
- Removing the `12` pin from the golden set without recording the set again.
