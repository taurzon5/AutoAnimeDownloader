# Max Episodes per Anime as a switch, off by default

Found on 2026-11-25 while noting what the test instance should do differently. Not started.
Delete this file when it is fixed or dropped.

## What the user asked

`max_episodes_per_anime` ("Max Episodes per Anime" on the Configuration page) should be something
the user can turn off, and it should be **off by default**. Not tied to a release yet.

## What exists today

- Saving `0` already turns it off: no ceiling and no end to the pack window (`effectiveMax`,
  `windowEnd` in `src/internal/daemon/episodes.go`). But the page shows only a number field
  (`id="max_episodes_per_anime"` in `src/internal/frontend/src/routes/Config.svelte`), with "Set to
  0 for no limit" in its hint.
- The default is `12`: `DefaultConfig` in `src/internal/files/filemanager.go`, and the page's
  fallback config in `Config.svelte`.
- In direct mode with `auto_delete_downloads` off (keep forever) the setting is ignored
  (`Config.LiftsEpisodeLimit`), yet the field still shows as if it applied.

## What to check

- **The switch.** A toggle "Limit episodes per anime" with the number under it only when on, like
  the delete-watched toggle and its amount. Keeping `0` as off in `config.json` means
  `config set max_episodes_per_anime 0` and old configs keep working; a separate bool would not.
  Hide or grey the field out when keep forever ignores it.
- **Off by default opens the pack window.** With no limit, a long series resolves many packs in
  one pass (One Piece about 14), and the brakes left are `max_batch_torrent_size_gb` and the disk
  guard (`checkRootBudgetSpace`), as the `MaxBatchTorrentSizeGB` row of
  [config.md](../agents/config.md) says. Every aired episode of every list entry downloads.
- **Existing installs keep their saved value.** `LoadConfigs` unmarshals over the defaults, so a
  saved `12` stays `12`. Only new installs, and configs without the key, get off. Say so in the
  release notes.
- **The golden set runs on `DefaultConfig()`** (`src/internal/daemon/golden_test.go`). With the
  window open its picks can move: pin `12` there or record the set again. Same check for other
  tests that lean on the default 12.
- **What else reads it:** the `episode_limit_reached` notification (never fires when off),
  [decisions/048](../agents/decisions/048-max-episodes-per-anime-never-limited-batches-the.md) and
  [decisions/137](../agents/decisions/137-direct-mode-deletes-through-the-torrent-and-keep.md),
  the hint in `en.json`, and the `MaxEpisodesPerAnime` row of `config.md`.
- **Together with** [direct-mode-by-default.md](direct-mode-by-default.md): direct mode with keep
  forever already ignores the limit, so if both land, a fresh install has no limit either way.
