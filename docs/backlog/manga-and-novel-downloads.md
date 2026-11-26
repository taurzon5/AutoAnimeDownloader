# Download manga and light novels (CBZ, CBR and EPUB), from the watchlist and beyond it

Found on 2026-11-25 while noting what to work on later. Not started. Delete this file when it is
fixed or dropped.

## What the user asked

- A separate download section for **CBZ** and **CBR** (manga) and **EPUB** (light novel) files.
- Fed by the watchlist, as anime are: the titles on the user's reading lists. But only **partly**
  (added later the same day): any title's releases can be set to **download automatically**,
  whether or not it is on a list.
- Also **all available downloads** for a title: every release, to pick one by hand.
- When several releases fit, pick the one that covers the **most releases** (volumes or chapters),
  or the **biggest** (higher quality).

Not tied to a release yet.

## What exists today

- Anime only. Every AniList list query asks for `type: ANIME` (`GetAllCurrentAnime`,
  `GetFrontendAnimeList`, `GetCustomListsMap` in `src/internal/anilist/anilist.go`). AniList keeps the
  same lists for `MANGA`, where light novels are format `NOVEL`, and an entry's progress is
  `progress` (chapters) plus `progressVolumes`. MyAnimeList has a manga list of its own.
- Nyaa is searched in category `1_2` only, Anime English-translated
  ([sources.md](../agents/sources.md)). Manga and novels are under Literature (`3_x`; check which
  sub-categories hold English-translated and raw releases).
- Search, matching, the pack window, the ranking (Priorities page) and the library layout are all
  built around episodes.

## What to check

- **What is tracked: volumes or chapters.** Releases name volumes (`v01-12`, "Vol. 1-20") or
  chapters (`c001-150`), and AniList counts both. Decide which one "downloaded" and "behind" count,
  and how a volume pack covers chapters.
- **The pick rule.** Among the releases that fit, most coverage first, then the biggest size. Decide
  whether that is its own simple ranking or a set of lists on the Priorities page (groups, digital
  versus scans).
- **All available downloads.** The torrent picker for a title, listing every Literature result, not
  only packs.
- **Formats.** CBZ, CBR and EPUB as asked. Decide what happens to a release in PDF or AZW3:
  skipped, or accepted and said so.
- **Automatic download beyond the watchlist.** A per-title switch ("download new releases
  automatically"), on by default for titles a list brings and settable on any other, so the
  watchlist is one source among others, as standalone anime are today
  ([decisions/049](../agents/decisions/049-standalone-anime-tracked-by-the-list-is-the.md)). Decide
  whether it can also follow one release line (a group's volumes) rather than the title, and whether
  a list title can be switched off without leaving the list.
- **Library.** A root of its own, for a reader server (Komga, Kavita, Calibre-Web) rather than
  Jellyfin, with a folder per series and a file per volume. `download_roots` and the disk guard
  apply; manga batches can be tens of GiB, so a size ceiling of its own.
- **The section.** Its own sidebar page with a Status-like list, and an Add screen for a title on no
  list, as anime have.
- **Titles.** Search by the AniList manga titles.
  [decisions/132](../agents/decisions/132-season-and-part-are-read-from-the-synonyms-that.md) keeps
  manga and novel synonyms out of anime searches; this needs the reverse.
- **CLI parity** for every endpoint ([conventions.md](../agents/conventions.md)).
- **Related:** links gathered from pages in the in-app browser could feed this section too
  ([in-app-browser.md](in-app-browser.md)).
