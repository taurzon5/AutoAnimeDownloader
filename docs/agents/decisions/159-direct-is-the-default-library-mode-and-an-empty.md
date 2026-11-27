# 159. Direct is the default library mode, and an empty mode becomes direct only where config comes in

**Location:** `internal/files/filemanager.go` — `DefaultConfig` (`LibraryMode: LibraryModeDirect`),
`LoadConfigs` (an empty `library_mode` becomes `"direct"`), `Config.IsDirectMode` (tests for
`"direct"` only); `internal/api/endpoint_config.go` — `handleUpdateConfig` (the same fill-in before
validation); `frontend/src/routes/Config.svelte` — the fallback config. Pinned by
`TestEmptyLibraryModeLoadsAsDirect`, `TestSaveConfig_EmptyLibraryModeSavesDirect`; the golden set
(`runGoldenPass`), `TestRegisterPlacement_OnlyInDirectMode` and
`TestShokoStatusCarriesShokosFolders` pin hardlink where they mean it.

**What it looks like:** two places fill in `"direct"` for an empty value, while `IsDirectMode`
itself still treats an empty value as hardlink. The obvious cleanup is one rule in `IsDirectMode`:
`LibraryMode == "direct" || LibraryMode == ""`.

**Why it's right:** on 2026-11-24 the user decided that `library_mode` should default to
`"direct"`: the app is built for a new library in direct mode, and hardlink mode gets no new work.
So a new install, and a `config.json` with no `library_mode` key, is in direct mode. Before direct
mode existed (the original project, v2.11.3 and older) the key did not exist, so an old config
without it now loads as direct. The only install of this repo that predates the change, the
user's own, has `"library_mode": "hardlink"` saved, so it keeps hardlink mode.

Putting the rule in `IsDirectMode` was tried and broke a dozen tests: they build a `files.Config{}`
literal with no mode and mean hardlink (the episode limit applies there, and direct mode with keep
forever lifts it). In code the zero value stays hardlink; what comes from outside, a file or a
`PUT /config`, is filled in at those two entry points. GET /config then always returns a mode, so
the settings page, which tests for `"direct"`, agrees with the daemon.

`auto_delete_downloads` keeps its default `false` (keep forever), as the user's other choices for
a new install go the same way ([#158](158-the-episode-limit-stays-one-number-with-0-as-off.md):
no episode limit). With direct mode and keep forever a new install downloads every aired episode
of every anime on its lists and deletes nothing on its own. `shoko_full_packs` defaulting to on was
argued from exactly this combination
([#149](149-a-pack-chosen-for-the-missing-episodes-records-the.md)), so it still holds.

The golden set was recorded in hardlink mode with the limit at 12 and pins both.

**Don't "fix" by:**
- Moving the empty-is-direct rule into `IsDirectMode`: every `files.Config{}` literal in the tests
  turns into direct mode and loses the episode limit.
- Dropping the fill-in from `LoadConfigs`: GET /config would return `""`, the settings page would
  show hardlink, and the daemon would run hardlink mode for a config the user never set to it.
- Dropping it from `handleUpdateConfig`: the handler validates and applies the decoded body before
  anything reloads it (`validateTorrentClient` asks for direct mode, the probe picks
  `ProbeWritable` or the hardlink probe, `SetDirectMode` rebuilds the session), so an empty mode
  would run all of that as hardlink. It is the one load-time rule `PUT` repeats, the exception
  [#90](090-there-is-no-configjson-versioning-the-migration.md) names.
- Putting the default back to hardlink: the user chose direct; hardlink mode gets no new work.
