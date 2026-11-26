# A General group for settings that aren't about the library or downloads

Found on 2026-11-25 in the UI and settings review (finding 16). Not started. Delete this file
when it is fixed or dropped.

## Symptom

Library, where the first run sends people, mixes folder settings with app and display preferences:
"Check for updates", "First steps · Show again", "Keep Cards of Removed Torrents" (only the anime
page's Torrents section) and the "Recently Downloaded Shelf" (Downloads).

## What to check

- A "General" (or "Interface") group for those four.
- Links that open a group by name (`#/config?group=`) and the tests that click group buttons.
