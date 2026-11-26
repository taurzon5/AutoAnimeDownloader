# qBittorrent's queue and seeding settings live apart from it

Found on 2026-11-25 in the UI and settings review (finding 8). Not started. Delete this file
when it is fixed or dropped.

## Symptom

Max Concurrent Downloads and Disable seeding (with its upload limit) are in Downloads, while "Let the
App Manage This qBittorrent's Settings", which uses them, is in Torrent client. The hints point across
the gap, and someone setting up qBittorrent has to switch groups to see what it will change.

## What to check

- Move Max Concurrent Downloads and Disable seeding into Torrent client, next to the manage switch,
  or repeat a read-only summary there.
- They apply to the built-in client too: decide where they live when qBittorrent isn't used.
