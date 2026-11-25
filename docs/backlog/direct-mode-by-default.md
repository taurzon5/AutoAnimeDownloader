# Direct mode as the default library mode

Found on 2026-11-24 while weighing Shoko pack tracking against the two library modes. Not
started. Delete this file when it is fixed or dropped.

## What was decided

The user wants `library_mode` to default to `"direct"` instead of `"hardlink"`. The app is being
built for a new library in direct mode: hardlink mode gets no work, and moving an existing install
over to direct is out of scope. Not tied to a release yet.

## What to check

- **What an empty value means.** `IsDirectMode` tests for `"direct"`, and `config.md` says an
  empty `library_mode` means hardlink. With direct as the default, empty should probably mean
  direct too.
- **Keep forever comes with it.** `auto_delete_downloads` defaults to `false`, so in direct mode
  `LiftsEpisodeLimit` ignores `max_episodes_per_anime` and nothing is deleted automatically. A
  fresh install would download every aired episode of every anime on its lists. Decide whether
  that is wanted or whether `auto_delete_downloads` changes default with it.
- **The golden set runs on `DefaultConfig()`** (`daemon/golden_test.go`). Direct mode lifts the
  episode limit and opens the pack window, so its picks can move. Pin hardlink mode there, which
  is what the direct-library-mode spec's test list expects, or record the set again.
- **Tests that assume hardlink mode** through `DefaultConfig()`: the `.torrents` download path,
  the hardlink probe when `completed_anime_path` is saved (direct mode only probes for writing).
  `grep -rn 'DefaultConfig()' src --include=*_test.go`.
- **Docs that call hardlink the default:** the `LibraryMode` row and the "Download Path" section of
  `docs/agents/config.md`, `docs/guides/webui.md`, and decisions that say so
  (`grep -ril hardlink docs/agents/decisions/`). `docker/entrypoint.sh` already sets direct mode
  for the qBittorrent profile.
- **Download Full Megapacks** (`shoko_full_packs`,
  [decisions/149](../agents/decisions/149-a-pack-chosen-for-the-missing-episodes-records-the.md))
  already defaults to on, argued from direct mode with keep forever (no slot, no duplicate). If
  `auto_delete_downloads` changes default with this, check that the argument still holds.
