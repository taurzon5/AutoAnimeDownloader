# Switching the torrent client asks for a restart only in a toast

Found on 2026-11-25 in the UI and settings review (finding 5). Not started. Delete this file
when it is fixed or dropped.

## Symptom

After a save that changes `torrent_client`, the only notice is a 4-second toast ("Saved. Restart the
app to switch the torrent client."). Afterwards nothing on Settings or Status says the app still runs
the old client; Status has no restart button.

## What to check

- Report the running client in `GET /status`, and show a persistent "restart needed" line under the
  switch, plus a banner, while it differs from the saved one.
- A new status field ships with the CLI's `status` output (CLI parity).
