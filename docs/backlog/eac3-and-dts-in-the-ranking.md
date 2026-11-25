# E-AC-3 ranks as AC3 or as nothing, and DTS isn't read by the ranking

Found on 2026-11-24 while writing `nyaa.ParseRelease` (release-filters spec, step 1). Not started.
Delete this file when it is fixed or dropped.

## Symptom

The ranking's audio criterion reads the codec with `extractAudio` (`src/internal/nyaa/nyaa.go`,
patterns `reAudioPatterns` in `nyaa_regex.go`), first match wins:

- `EAC3` / `E-AC3` (Dolby Digital Plus) matches the `AC3` pattern, so it ranks as AC3, below AAC,
  instead of as `ddp`, above it. The spelling `E-AC-3` matches no pattern: it ranks as having no
  audio tag.
- A plain `DTS` isn't read: a DTS-only name ranks as having no audio tag.

`ParseRelease` reads both right (its own list, `releaseAudioCodecs`), so the anime page shows the
true codec while the ranking uses the old one.

## Why it wasn't fixed with step 1

Both change picks, and step 1 of the spec promises none beyond its planned fixes (decisions/150):

- E-AC-3 as `ddp` lifts those releases above AAC.
- A `DTS` pattern before AAC turns a "DTS + AAC" name into DTS. `dts` isn't in the default audio
  list (`priorities.go`), so that name would rank below MP3, where today it ranks as AAC.

## What to check

- Add `dts` to the default `Audio` list (and decide where), or read DTS only when no other codec
  is in the name.
- Replay the golden set before and after; look for E-AC-3 and DTS names among the movie and
  finished-series cases, where the audio criterion breaks ties.
