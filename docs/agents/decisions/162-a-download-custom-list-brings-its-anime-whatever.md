# 162. A download custom list brings its anime whatever their list status, read from MediaListCollection, and vetoes status deletion

**Location:** `internal/files/filemanager.go` — `Config.DownloadCustomLists`, `CustomListSource`,
`CustomListsOf`; `internal/anilist/customlists.go` — `GetCustomListMembers`;
`internal/daemon/verification.go` — `customListExtras` (called from `searchAnilist`),
`customListedMedia` and `withoutCustomListed` (after `deletableMediaIDs`), `handleSavedEpisodes`
(`customListed`, `customListsGone`); `internal/anilist/persist.go` (the membership cache is in the
snapshot); `internal/api/endpoint_animes.go` —
`customListEntries` (from `fetchAniListEntries`, so the Status page and the standalone guard).
Pinned by `TestSearchAnilist_DownloadCustomListBringsAnyStatus`, `TestWithoutCustomListed`,
`TestWithoutCustomListed_UnreadableListPausesStatusDeletion`,
`TestSaveConfig_DownloadCustomListsNeedAccountAndName`.

**What it looks like:** a second way into the pass next to `download_statuses`, read through a
different AniList query (`MediaListCollection`) than every other list, plus a batched media lookup
for its anime. The obvious simplification is to drop `status_in`, or to query the statuses
`download_statuses` leaves out with the same `GetAllCurrentAnime` and `GetCustomListsMap`.

**Why it's right:** the user asked on 2026-11-25 that every anime in an AniList custom list
download, and decided on 2026-11-26 that its list status doesn't matter: Planning, Paused,
Completed and Dropped download too. The media-status filter still applies (an anime that hasn't
aired has nothing to download), and an anime also in an excluded list stays out, per episode
(`animeIsInExcludedList`): a member carries every custom list it is in, not only the requested
ones. A Completed member outside `download_statuses` counts progress 0 in the pass and in
`resolveAnimeDetails` (so the debug run selects the same, [#58](058-runanimedebug-mirrors-processanimeepisodes.md)):
its progress is the total and would read every episode as watched. That 0 wins the dedupe on
purpose, the list asking for the whole anime. Paused and Dropped keep their progress; the Status
page shows the real one.

The cost decides the query. The first version fetched the four other statuses through
`GetAllCurrentAnime` and `GetCustomListsMap`: ceil(N/50) requests each, every pass, and the same on
every Status poll. On the list [#130](130-status-deletion-asks-anilist-only-about-the-animes.md) measured
(1124 entries, 23 pages) that is about 46 requests a check, which is the 429 that entry describes.
`MediaListCollection` returns the account's lists in chunks of 500 and is asked only for each
entry's id, status, progress and list name: ceil(N/500) requests (3 for 1124 entries), cached
`customListsTTL` (5 minutes) per account and lists, kept in the disk snapshot, a failure with no cached copy remembered
for a minute (the pass asks twice), an empty member set kept only 30 seconds and a collection with
no list at all read as a failure (a degraded answer must not lift the deletion veto), and served
expired when AniList fails (with `ErrFromCache`) or the budget gate refuses (with no error,
[#72](072-anilist-budget-is-measured-from-the-response.md)). Only the anime in a status outside
`download_statuses` then need their media, through `GetMediaByIDs` (50 per request, cached per id
for 60 seconds, the lookup standalone anime use): ceil(K/50) per pass, and per minute while a
Status tab is open, as for standalone anime. An account with no download custom list costs nothing
more.

Download is OR and deletion is AND ([#43](043-an-animes-identity-is-mediaid-status-is-a-per.md)):
a custom-list anime held in a delete status by every account would be downloaded and then deleted
by status on every pass. Being in the list is the user's choice to have it, so it vetoes status
deletion like an account holding the anime in a neutral status. Nor is a listed anime deleted as
"not in watching" when it is missing from the pass (its media not fetched this time). With an
account's lists unreadable and no cached copy, both deletions wait for the next pass.

On the Status page a membership or media read that fails keeps the account on screen with its main
list, marked as from the cache, so the orphan refresh stays off
([#42](042-every-persisted-filemanager-state-writes-with.md),
[#92](092-the-on-disk-anilist-cache-restores-with-ttl-zero.md)) without taking the account down
for the cheap query ([#72](072-anilist-budget-is-measured-from-the-response.md)).

**Don't "fix" by:**
- Dropping `status_in`, or querying the other statuses with `GetAllCurrentAnime` and
  `GetCustomListsMap`: two paginated full-list fetches per pass and per poll bring back the 429.
- Letting status deletion reach a custom-list anime: it downloads and deletes it every pass.
- Reading a bare list name without its account: the name belongs to one account, and a bare one
  would match a list of the same name on every account.
- Treating a failed membership read as "no extras": on the Status page the orphan refresh would run
  on partial coverage, and in the pass the listed anime would be deleted as "not in watching".
- Keeping the requested list names only on a member: the excluded-list check would miss it.
- Using the entry's own progress for a Completed member: it downloads nothing.
- Retrying a failed membership read in the same pass, or trusting an empty collection: the first
  burns budget, the second lets status deletion reach the listed anime.
