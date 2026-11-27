# 69. `codec` now applies to episodes, and a name with no codec tag ties instead of losing

`episodeCriteria` (`nyaa/priorities.go`) filters `CriteriaOrder` to the criteria that make sense
on an episode, and `codec` was **out**: the `priorities.codecs` list showed up on the Priorities
screen and in `config.json` without affecting any episode download — only `SortMovieResults` read
it. Because the default is `["hevc","av1",...]`, the episode codec was decided in passing by
`fansub` (Judas/Ember are x265 10-bit; SubsPlease/Erai-raws are H264), and the user had no lever.
Whoever watches through a client with no HEVC decode (a browser — Chrome and Firefox do not ship
HEVC for licensing) pays that as a transcode on the server, which besides burning CPU **burns the
ASS subtitle into the video** and desyncs it. `codec` is now in `episodeCriteria`.

**Source stays out of `episodeCriteria` (the RELEASING / weekly sort).** It enters the series sort
only through `SortFinishedSeriesResults` when `Media.Status == FINISHED` (decision #107). A weekly
episode is WEB-DL on practically every release; the criterion would never break a tie and would
only spend a comparison.

**The guard that keeps the change from backfiring:** `priorityIndex` returns `len(list)` for an
unknown token, i.e. **the worst**. And `ExtractCodec` returns `""` for a name with no codec tag —
which is exactly SubsPlease's pattern (`[SubsPlease] Anime - 07 (1080p) [HASH].mkv` does not say
H264 anywhere). With `codec` before `fansub` in the default order, turning the raw criterion on
would make every untagged release lose to any tagged x265: the **opposite** of what the codec list
configures. That is why `codecCompare` returns a tie when either of the two names has no tag,
leaving the next criterion to decide. It is the same rule `sizeCompare` applies (it compares sizes
only between two releases of the same known resolution), and the same as `filterBySize`
(`nyaa.go`): data that could not be read is not a reason to punish the release. A missing
**resolution** is the exception: `resCompare` ranks a release that shows none after one that does.

**Canonical tokens.** `ExtractCodec` only returns `HEVC`, `AV1`, `H.264` and `XviD`
(`reCodecPatterns` maps `x265`→`HEVC`, and `x264` and `AVC`→`H.264`). The `"x265"` and `"x264"`
entries an old `config.json` may still carry in `priorities.codecs` therefore **never match
anything** — they are inert; the default no longer lists them. Configuring a preference for H264
requires writing `h.264`, not `x264`.

**Don't "fix" by:** taking the `codecCompare` guard off "because no tag is no tag"; putting
`source` in `episodeCriteria` for weekly airing (finished series use the dedicated sort); moving
`codec` after `fansub` in the default to work around the unknown-token problem (hides the same bug
instead of solving it).

**Amendment (see [#150](150-what-a-torrent-was-is-recorded-once-per-info-hash.md)):** `AVC` now
reads as H.264. Erai-raws writes `[1080p CR WEB-DL AVC AAC]`, so its names carry a codec and no
longer get the tie: the codec list decides between them and a tagged release, where `fansub` used to.
