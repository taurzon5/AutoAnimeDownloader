# Delete settings look active while direct mode keeps everything

Found on 2026-11-25 in the UI and settings review (finding 3). Not started. Delete this file
when it is fixed or dropped.

## Symptom

With `auto_delete_downloads` off (the default, keep forever), `withoutKeptForever`
(`src/internal/daemon/episodes.go`) drops every direct-mode torrent from watched pruning and from
Delete Statuses. Yet "Delete watched episodes automatically" ships on and shows active, "Watched
Episodes to Keep" and "Delete Statuses" stay editable, and the four settings sit in three groups
(Downloads, Lists, Library). Only "Delete Downloads Automatically"'s own hint says nothing is deleted.

## What to check

- Gather them into one "Cleanup" block under "Delete Downloads Automatically", and dim them with a
  one-line note while direct mode is on and auto delete is off.
- Moving fields changes which group a save error opens (`requiredChecks` groups) and the tests that
  open a group by name.
