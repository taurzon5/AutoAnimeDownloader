# 161. Priorities and notifications save only their part, merged into the saved one, and skip the full save's checks

**Location:** `internal/api/endpoint_config.go` — `handleUpdatePriorities`,
`handleUpdateNotifications`, `readPartBody`, `savePart`; `internal/api/server.go` —
`Server.configMu`, also taken by `handleUpdateConfig` and `handleShokoLogin`
(`endpoint_shoko.go`); the Priorities and Notifications pages (`updatePriorities`,
`updateNotifications` in `lib/api/client.ts`); CLI `priorities set`, `notifications set`. Pinned by
`TestUpdateConfigParts` and `TestConfigPartsSetSendTheirPartOnly`.

**Amendment (see #164):** a third PUT saves the priorities the same way: `PUT
/queues/default/priorities` (`handleQueuePriorities`, `endpoint_queues.go`) goes through
`savePart` with the same merge, since the default download queue's ranking is `config.json`'s
`priorities`. The other queues' rankings live in `queues.json` and don't touch `config.json`.

**What it looks like:** two PUTs that write `config.json` with none of `PUT /config`'s validation
(the folder probes, the download-folder checks, the qBittorrent and Shoko checks), and a body that
is decoded over the saved value instead of replacing it. Two obvious "fixes": run the full checks
in `savePart` for consistency, or make `PUT /config` keep `priorities` and `notifications` from disk
so a stale Settings tab can't revert them.

**Why it's right:** the UI review of 2026-11-25 found that the two pages sent the whole config they
had loaded, so `handleUpdateConfig` ran every rule: with one download folder's drive unplugged,
reordering fansubs failed with an error about a folder on another page. The checks belong to the
fields they check, and these two parts have none of those fields; the batch window is the one rule
that touches them, and the notifications PUT keeps it.

The body is decoded over the saved part, so a key it leaves out keeps its value: a file with only
`codecs` must not empty every other list (`LoadConfigs` brings defaults back only for an absent
key, not for a saved `null`), and a `webhooks: null` is saved as an empty list, which is what the
Notifications page reads. `configMu` serializes every read-change-save of the config (the full PUT,
the two partial ones, the Shoko sign-in), so two at once can't drop one write. It does not stop a
Settings tab opened earlier from writing back the priorities it loaded: `PUT /config` still writes
every field it is sent.

**Don't "fix" by:**
- Running `PUT /config`'s checks in `savePart`: the unplugged-drive refusal comes back on pages that
  have no folder fields.
- Making `PUT /config` keep `priorities` and `notifications` from disk: `config set
  priorities.<list>` and `config set notifications.*` go through the full PUT and would silently
  stop working.
- Decoding the partial body into an empty struct: a part left out of the file is saved as empty.
- Saving the config outside `configMu` in a new handler.
