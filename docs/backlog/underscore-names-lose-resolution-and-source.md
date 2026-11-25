# Underscore-joined release names get no resolution and no source

Found on 2026-11-24 while writing `nyaa.ParseRelease` (release-filters spec, step 1). Not started.
Delete this file when it is fixed or dropped.

## Symptom

`(Hi10)_Gosick_-_01_(BD_720p_AAC)` reads no resolution and no source: `ExtractResolution` and
`ExtractSource` (`src/internal/nyaa/nyaa.go`, patterns in `nyaa_regex.go`) wrap their tokens in
`\b`, and `_` is a word character, so `_720p_` and `(BD_` never match. The same name with spaces
reads 720p and BD.

## Cause

`\b` is the word boundary of `[0-9A-Za-z_]`. Older groups and some batch uploads join every word
with underscores.

## What to check

- How often underscore names still turn up (the 669-name sample of 2026-11-23 had none on page 1).
- The fix changes ranking: a release with no resolution loses to any release that has one
  (`resCompare`), so an underscore name would start winning where it ranks well. Replay the golden
  set (`go test ./src/internal/daemon -run TestGoldenSet`) before and after.
- Replacing `\b` with `(?:^|[^A-Za-z0-9])` / `(?:[^A-Za-z0-9]|$)` in the resolution and source
  patterns, or normalizing `_` to a space once before parsing.
