---
name: release
description: Ship an AutoAnimeDownloader version end to end — check nothing done is left in TODO.md or docs/backlog, stamp TODO.md, tag and push, wait for the GitHub Release workflow and check its assets. Use ONLY when the user explicitly asks to release, publish, tag or stamp a version (e.g. "release 2.9.0", "release v2.9.0", "/release 2.9.0"). Never use it on your own initiative, to "prepare" or "check" a release, or because a TODO section looks finished.
---

# Release a version

Takes the version as argument: `$ARGUMENTS` (e.g. `2.9.0` or `v2.9.0`). Below, `X.Y.Z` is the
number without `v`, `vX.Y.Z` the tag. No version given → read `docs/TODO.md`, propose the number
(rule below) and ask; never pick one silently.

This skill pushes a tag to `origin`, which publishes a GitHub Release — something that can't be
quietly undone. Being invoked is **not** the permission: the confirmation before the push waits for
an explicit yes in that message. Anything unexpected along the
way — dirty tree, failed workflow, missing checksum — stop and report; don't work around it.

## 0. Pre-flight (read-only)

```bash
git rev-parse --abbrev-ref HEAD          # must be master
git status --porcelain                   # must be empty
git fetch origin && git status -sb       # must not be behind origin/master
git tag -l "vX.Y.Z"                      # must be empty
go test ./...                            # must pass
```

Release from the **main checkout** on `master`, not from a worktree or a feature branch. If the
user has other work going on in that checkout, ask before touching it.

**Nothing done is still listed.** Read `docs/TODO.md` and `ls docs/backlog/` against the commits
going out (`git log --oneline "$(git describe --tags --abbrev=0)"..HEAD`). An item those commits
already deliver — a TODO bullet outside the section being shipped, or a backlog file that should
have been deleted with its fix — is reported to the user with the commit that did it; ask before
deleting it. Don't stamp with leftovers.

**The number** comes from `docs/TODO.md`: each section is one release, its bump is the largest bump
of the tasks inside it. A task that doesn't touch the binary rides along without changing the
number. Commits with no section (a backlog fix, a fix found on the way) set the bump by their type:
`feat` → minor, `fix` → patch, `!` → major. If the requested number disagrees, say so and ask.

## 1. Stamp the TODO

In `docs/TODO.md`:
1. `Current version:` → `` `vX.Y.Z` ``.
2. **Delete** the shipped section, whole. No "released in" line stays behind: the GitHub Release
   notes and git history are the record.

Commit, subject line only (CLAUDE.md commit style):

```
docs: stamp vX.Y.Z and drop what it shipped from the todo
```

## 2. Confirmation — then tag and push

Show the user: the stamp commit (`git show --stat HEAD`), the commits going out
(`git log --oneline origin/master..HEAD`) and the tag name. Ask: *"Push master and tag vX.Y.Z?"*
Wait for an explicit yes. On yes:

```bash
git tag vX.Y.Z                 # lightweight, on the stamp commit
git push origin master
git push origin vX.Y.Z
```

Pushing a `v*` tag is what starts `.github/workflows/release.yml`; there is nothing to click on
GitHub. A wrong stamp commit is fixed with a new commit and a new tag — never move or delete a
pushed tag without the user asking.

## 3. Wait for GitHub Actions

```bash
gh run list --workflow=release.yml --limit 1          # find the run for vX.Y.Z
gh run watch <run-id> --exit-status                   # run_in_background: true
```

The workflow builds Linux amd64, Linux arm64 and Windows; commits
`chore: update download links to vX.Y.Z` on `master` when the README asset names change; publishes
the Release with the zips, the Windows exe, their `.sha256` files and generated notes. Failed →
stop, show the failing job's log (`gh run view <run-id> --log-failed | tail -50`), and ask.

Then `git pull --ff-only origin master` (the workflow may have committed) and check that the
Linux checksums were published:

```bash
for a in Linux_x86 Linux_Arm64; do
  curl -sfIL -o /dev/null -w "%{http_code} $a\n" \
    "https://github.com/taurzon5/Nyaanchiver/releases/download/vX.Y.Z/AutoAnimeDownloader_${a}_vX.Y.Z.zip.sha256"
done
```

Both must be `200`. Anything else → the Release is missing assets: stop and report.

## Out of this flow

There is **no AUR step**. The AUR packages `autoanimedownloader-bin` and `autoanimedownloader-git`
belong to the original project, not to this repo; its own packages come with the rename
(`docs/v1.0.0.md`, D7 and R24).

Tracked elsewhere, not part of a stamp: the ghcr image (`v1.0.0` in TODO.md; the Dockerfile is
built in CI and never pushed — see `docs/distribution.md`), the Windows installer/service
(`v1.1.0`), and the CLI inside the Windows package (`package-windows` doesn't ship it).

## Report

The tag, the Release URL (`gh release view vX.Y.Z --json url -q .url`), and whether the workflow
committed the README links.
