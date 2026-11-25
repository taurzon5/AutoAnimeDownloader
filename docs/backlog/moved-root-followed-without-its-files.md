# A download folder "moved" by a rename is followed even when its files aren't there

Found on 2026-11-24 while making sure reordering download folders moves no file. Not started.
Delete this file when it is fixed or dropped.

## Symptom

Direct mode, several `download_roots`, the built-in client. The user renames folders so a name now
points at a different existing folder: `ssd` was `/mnt/a`, now `ssd` is `/mnt/b` (for example, two
rows edited in place instead of using the ↑/↓ buttons, which swap whole rows and are safe). Nothing
happens while `/mnt/a` is there. Later `/mnt/a` goes away (a drive unplugged or not mounted at
boot): every torrent placed on `ssd` is pointed at `/mnt/b`, re-added, finds no files and downloads
everything again there. When `/mnt/a` comes back, the episodes exist twice and the records point at
the new copies.

## Cause

`followMovedRoots` (`src/internal/daemon/placement.go`) follows a root by name
([decisions/154](../agents/decisions/154-several-download-roots-each-placement-records-its.md),
"A moved root is followed by name") once the old folder is gone and the name points at another
existing folder. It never checks that the torrent's files are at the new path.
`Session.MovePlacementRoot` then re-adds the torrent with no saved progress, so rain verifies
against the new root and downloads what is missing. qBittorrent keeps its save path, so it only
re-points the placement there, and the torrent waits instead of downloading again.

`adoptMovedStaging`, the same follow for a moved incomplete folder
([decisions/141](../agents/decisions/141-staged-downloads-are-copied-into-the-library-while.md)),
already checks first: it re-points only when the torrent's `<hash>` folder is in the new place.

## What to check

- Follow only when the placement's files are found under the new root: a non-skipped file of
  `p.Files` at its final path (or its `.part` name while unfinished), relative to the new root. A
  placement with no files yet has nothing on disk and can follow as it does now. One whose files
  are in the incomplete folder (`IncompleteRoot`, not `Completed`) has nothing in the root yet
  either.
- When the files aren't there, leave the placement alone and log it once, like `adoptMovedStaging`
  does ("waits for it to come back"), so a moved drive whose files didn't come along is visible.
- Pin it next to `TestFollowMovedRoots` and `TestReorderingRootsMovesNothing` in
  `src/internal/daemon/roots_test.go`: renamed root plus old folder gone, with and without the files
  at the new path. Then amend decision 154's "followed by name" bullet and its "Don't fix by" list.
