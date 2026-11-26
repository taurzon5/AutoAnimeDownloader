# The Shoko match form sits open on every anime page

Found on 2026-11-25 in the UI and settings review (finding 29). Not started. Delete this file
when it is fixed or dropped.

## Symptom

With Shoko on, every anime page shows the series id and offset inputs and "Confirm match", even for a
clean automatic match (`ShokoMatchPanel.svelte`). With the accounts panel and the folder picker it
pushes the episode list down. "Offset" has no hint about which number it expects.

## What to check

- Collapse the manual form behind "Correct the match" unless the match is partial or none.
- A hint for Offset: the Shoko episode number of this anime's episode 1, minus 1.
