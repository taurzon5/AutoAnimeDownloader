# Saving priorities or webhooks re-checks the whole configuration

Found on 2026-11-25 in the UI and settings review (finding 35). Not started. Delete this file
when it is fixed or dropped.

## Symptom

Priorities and Notifications PUT the full config they loaded, so `handleUpdateConfig` runs every rule,
including the folder probes, the download-folder checks and the qBittorrent and Shoko checks. With a
download folder's drive unplugged, reordering fansubs fails with an error about a folder on another
page.

## What to check

- `PUT /config/priorities` and `PUT /config/notifications` that write only their part, each with its
  CLI command in the same change (CLI parity), or skip the probes when no folder field changed.
- Concurrent saves from Settings and these pages: the partial PUTs must not overwrite each other.
