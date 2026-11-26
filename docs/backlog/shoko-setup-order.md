# Shoko: the switch comes before sign-in

Found on 2026-11-25 in the UI and settings review (finding 7). Not started. Delete this file
when it is fixed or dropped.

## Symptom

"Use Shoko" is the group's first row and sign-in the third. Turning it on and saving before signing
in fails on the server ("Sign in to Shoko before turning it on"), with no pending mark first. Watch,
refresh, full resync, megapacks and the path map stay editable while Shoko is off and signed out.

## What to check

- Address and sign-in first; the switch disabled until signed in; the dependent rows dimmed while
  it is off.
- A `requiredChecks` rule for "on but not signed in", in the UI's words.
- Full resync and Sign out already follow the saved status (finding 13 and 14, fixed).
