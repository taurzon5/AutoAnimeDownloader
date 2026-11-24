# 144. With qBittorrent the app's torrents never have a share limit, disable_seeding is an upload limit of a few KiB/s, and the queue is set only when the app manages qBittorrent

**Location:** `internal/torrents/qbittorrent.go`: `enforceLimits` (called by `poll`), `syncManagedSettings`
and `qbitWantedQueue` (called by `Ensure` and by `poll` when `queueDirty`), `SetMaxActiveDownloads`,
`SetUploadDisabled`, `QBitSettings.uploadFloor`, the `UploadLimit` given to `Add`.
`internal/torrents/qbit_client.go`: the share limits in `add`'s form, `setNoShareLimit`,
`setUploadLimit`, `qbitTorrent.hasShareLimit`, `qbitDefaultUploadFloorKiB`. Config:
`qbittorrent_manage_settings`, `qbittorrent_disable_seeding_upload_kib`
(`validateTorrentClient`). Pinned by
`TestQBit_AddGivesNoShareLimitAndTheUploadFloorOnlyWithDisableSeeding`,
`TestQBit_ShareLimitsAreTakenOffTheAppsTorrents`,
`TestQBit_ALimitQBittorrentIgnoresIsResentOnceAMinute`,
`TestQBit_DisableSeedingHoldsEveryTorrentAtTheUploadFloor`,
`TestQBit_AChangedUploadFloorAppliesAndIsLiftedLater`,
`TestQBit_ManagedQueueFollowsMaxConcurrentDownloads`, `TestQBit_UnmanagedQueueIsLeftAlone`,
`TestQBit_ManagedDiskIOIsSentOncePerConnection`, `TestQBit_RemoveDeletesTheIncompleteExtension`, and
`TestQBitLive` against a real qBittorrent ([testing.md](../testing.md#qbittorrent-live-test)).

**What it looks like:**
- The poll loop overwrites limits on the app's torrents that the user may have set by hand in
  qBittorrent.
- `disable_seeding`, a "no upload" switch, lets 16 KiB/s out by default instead of the smallest
  limit qBittorrent takes.
- `max_concurrent_downloads`, a plain app setting, does nothing with qBittorrent unless a second
  switch is on.

**Why it's right:**

- **No share limit, ever, on the app's torrents.** By default a torrent follows qBittorrent's
  global share limit (`-2`), and that limit's action can be "stop", "remove" or "remove with
  content". In direct mode a torrent's content *is* the library, so the last action deletes
  episodes the user keeps. Measured on qBittorrent 5.3.0 with a global ratio limit of 0: the
  torrent left on the global limit stopped within 2 s of finishing, and the one added with `-1`
  kept seeding. Every add therefore sends `ratioLimit`, `seedingTimeLimit` and
  `inactiveSeedingTimeLimit` as `-1`. The poll removes any other value from a torrent in the app's
  category: one taken over from another category, one dropped into the category by hand, or one
  changed in the WebUI. A limit set by hand is overwritten too, because the app owns its category
  and can't tell a safe limit from one that deletes the library. A seeding policy (ratio, days)
  would be an app setting, as the spec says.
- **`setShareLimits` sends five parameters.** qBittorrent 5.3.0 (Web API 2.16.2) answers 400
  without `shareLimitAction` and `shareLimitsMode`. Older versions don't have them and don't read
  them.
- **`disable_seeding` is an upload limit, and not a tiny one.** qBittorrent has no "upload
  nothing": an upload limit of 0 means unlimited, and so do 0 upload slots (`setPreferences` with
  `max_uploads: 0` saved `-1`). The upload limit also carries the download's own outgoing
  messages: about 17 bytes of request per 16 KiB block, plus a "have" to every connected peer per
  finished piece. A download therefore runs at most a few hundred times the limit. Measured on
  5.3.0 with a 7 MiB pack from a local seeder, by upload limit:

  | Upload limit | Download time |
  |---|---|
  | none | 2.6 s |
  | 16 KiB/s | 5.7 s |
  | 4 KiB/s | 7.2 s |
  | 1 KiB/s | 12.9 s |
  | 256 B/s | 40 s |
  | 1 B/s | never got its file list in 3 minutes |

  The spec's answer 3 ("the smallest limit qBittorrent accepts") would stop every download, so
  the user chose a setting, `qbittorrent_disable_seeding_upload_kib`, 16 KiB/s by default
  (2026-11-23). The rest of [#119](119-disable-seeding-means-no-upload-at-all-and-a.md) holds: the
  torrent is released once organized.
- **The floor is enforced every poll, and lifted by its value.** A torrent added before the switch,
  or changed in the WebUI, gets the floor on the next poll. Turned off, the floor comes off the
  torrents that carry the current floor or one set earlier in this run (`floorsUsed`), and a limit
  the user chose stays.
- **A limit qBittorrent doesn't take isn't sent on every poll.** The poll runs every 2 s.
  `shareSent`/`uploadSent` remember the limit last sent to a torrent and when, so the same limit
  goes out again only after `qbitLimitResend` (a minute). A different limit, such as a new floor,
  goes out at once. Keyed by time alone, a changed floor would wait a minute.
- **The queue is global, so it is set only when the app manages qBittorrent.** qBittorrent's queue
  caps the whole client, not a category, and the spec's answer 2 keeps global settings for the
  switch "Let the app manage this qBittorrent's settings" (`qbittorrent_manage_settings`, off by
  default for a shared qBittorrent). With the switch on, `max_concurrent_downloads` becomes
  `max_active_downloads` (0 → `-1`, no limit). `max_active_uploads` and `max_active_torrents` become
  `-1`: qBittorrent's defaults are 3 and 5, and those would queue seeding torrents (rain never
  limits seeding, [#41](041-the-download-queue-holds-every-incomplete-torrent.md)). Worse, with five
  torrents seeding, the total cap would hold the downloads back. The preferences are read on every
  `Ensure` and written only when they differ, so a queue changed in the WebUI goes back to the app's
  on the next pass. With the switch off, the connection test reports qBittorrent's own queue, and the
  settings page says which queue applies.
- **The switch also governs the rest of qBittorrent's global settings the app relies on.** These
  are answer 2's list, and only the keys that differ are sent:
  - **The ".!qB" extension is turned off.** The app names incomplete files itself
    (`incomplete_files`). Measured: with qBittorrent's extension on, the file on disk is
    `ep.mkv.part.!qB` while the API reports `ep.mkv.part`, and the `.!qB` is dropped when the
    torrent finishes. Off or on, `Remove` deletes the `.!qB` names too. Without that,
    `RemovePlacedFiles` would leave them, and the series folder they sit in could never be pruned.
  - **Default or memory-mapped disk I/O becomes "Simple pread/pwrite"** (`disk_io_type` 3).
    libtorrent 2's memory-mapped I/O takes the client down when a disk disappears under it (a USB
    library), and it is slow on network shares. POSIX (2) and the newer pread mode (4) are a choice
    the app leaves alone. The type needs a qBittorrent restart, and a version that doesn't take it
    would be sent it on every pass, so it goes out once per connection (`diskIOTried`). With the
    switch off, the connection test reports the type and the settings page recommends pread.

**Don't "fix" by:**
- Leaving a torrent on qBittorrent's global share limit, or respecting a per-torrent limit
  "because the user set it": either can remove the library's files.
- Dropping `shareLimitAction`/`shareLimitsMode` "because older versions don't know them".
- Setting the upload floor to 1 B/s "for less upload", or to 0 "for none" (0 is unlimited).
- Setting `max_uploads` to 0.
- Keying the resend memory by time only.
- Setting qBittorrent's queue without the switch.
- Leaving its upload and total caps at the defaults.
- Sending `max_concurrent_downloads` 0 as `max_active_downloads` 0.
- Sending every managed key on every pass instead of the ones that differ.
- Retrying the disk I/O type on every pass.
- Overwriting a POSIX or pread choice.
- Leaving the `.!qB` names out of `Remove`.
