# Settings labels use mixed casing and names that don't match

Found on 2026-11-25 in the UI and settings review (finding 18). Not started. Delete this file
when it is fixed or dropped.

## Symptom

- Sentence case ("AniList usernames", "Delete watched episodes automatically") next to Title Case
  ("Download Statuses", "Write Downloads Straight to the Library").
- "Anilist" in `config_hint_anilist_usernames`; "Excluded List" is singular for a list of lists.
- "Download Statuses" vs "Airing Statuses to Download" doesn't say which is the list status.
- The required field is "Completed Anime Path" while onboarding and errors say "the library folder".

## What to check

- One casing rule; "List Statuses to Download / Airing Statuses to Download / List Statuses to
  Delete", "Excluded Lists", "Library Folder".
- Hints and docs that quote the labels (webui.md, other hints) change with them; tests that find
  fields by label too.
