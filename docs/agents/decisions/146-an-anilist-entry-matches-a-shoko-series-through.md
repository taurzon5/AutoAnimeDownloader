# 146. An AniList entry matches a Shoko series through its MAL id, at offset 0 when it has the series' own episode count, and anything that doesn't add up waits to be confirmed by hand

**Location:** `internal/shoko/match.go`: `MatchEntry` and `OwnedOf`. Pinned by `TestMatchEntry`
(the Frieren case among them) and `TestOwnedOf`.

**What it looks like:**
- An anime with a MAL id in common with a Shoko series, which the user owns in Shoko, comes out as
  "partial" and counts as not in Shoko.
- A series listing several MAL ids is matched at offset 0 for one entry and at an offset for
  another.
- The app asks AniList for the episode counts of MAL ids it doesn't even track.

**Why it's right:**

- **The MAL id is the only bridge.** The app's anime are AniList entries and Shoko's series are
  AniDB anime. Every AniList entry has `idMal`, and every Shoko series lists its MAL ids, so no
  outside mapping file is needed (the Shoko spec §2).
- **A series' MAL ids are not only its cours.** Measured on Shoko v5.3.3: Frieren (AniDB 17617, 28
  regular episodes) lists MAL 52991, the series itself, and 56885, a set of shorts that AniDB files
  as specials. The spec's rule ("the offset is the sum of the episodes of the MAL ids listed before
  it") would have applied to every entry of such a series. So the rules are, in order:
  1. A series that lists the entry's MAL id alone is a match at offset 0.
  2. An entry with the series' own episode count is a match at offset 0.
  3. Otherwise the entry's episodes come after those of the MAL ids listed before it. That offset
     holds only when every listed MAL id's count is known and they add up to the series' count.
     The counts come from AniList.
  4. Anything else is **partial**, and so are two series listing the same MAL id.
- **A wrong match costs more than a missing one.** A wrong match means the app never downloads
  an episode the user doesn't have, and never says so. A missing one only downloads a duplicate.
  So a partial match owns nothing (`OwnedOf`) until the user confirms the series and the offset on
  the anime page (the user's answer 2: "check by hand").
- **Only regular episodes count.** Owned episodes are Shoko's `Episode` type with a file. An owned
  episode outside the entry's range after the offset belongs to another entry.

**Don't "fix" by:**
- Applying the offset sum to every series with several MAL ids (the Frieren case).
- Letting a partial match own episodes.
- Picking one of two series that list the same MAL id.
- Matching by title instead of the MAL id.
- Trusting the offset when one listed MAL id's count is unknown.
