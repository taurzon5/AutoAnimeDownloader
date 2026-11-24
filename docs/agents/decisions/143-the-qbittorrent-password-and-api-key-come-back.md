# 143. The qBittorrent password and API key come back from GET /config as a placeholder, and a PUT that sends the placeholder keeps them

**Location:** `internal/files/filemanager.go` — `SecretPlaceholder`, `Config.MaskSecrets`,
`Config.KeepSecrets`; `internal/api/endpoint_config.go` — `handleGetConfig`, the start of
`handleUpdateConfig`; `internal/api/endpoint_torrent_client.go` (the connection test takes the
saved secret the same way); `frontend/src/lib/api/client.ts` — `SECRET_PLACEHOLDER`;
`internal/diagnostics/bundle.go` — `redactConfig`. Pinned by
`TestGetConfig_MasksQBittorrentSecrets`, `TestUpdateConfig_PlaceholderKeepsTheSavedSecret`,
`TestTorrentClientTest` and `TestBuildRedactsTheQBittorrentCredentials`.

**What it looks like:** `GET /config` returns everything else as it is on disk, webhook URLs and
headers included, but `qbittorrent_password` and `qbittorrent_api_key` come back as `"__saved__"`.
And a `PUT /config` carrying `"__saved__"` doesn't save that string: it keeps what was saved.

**The placeholder only works for the saved address.** `KeepSecrets` refuses
(`ErrSecretNeedsRetyping`, HTTP 400) when the request's `qbittorrent_url` differs from the saved one
(ignoring a trailing slash and case): changing the address means typing the password and key again.
Otherwise anyone who reaches the web UI could point the address — or the connection test — at
their own server and receive the saved password. Pinned by
`TestUpdateConfig_PlaceholderWithAnotherAddressIsRefused` and
`TestTorrentClientTest_NeverSendsTheSavedPasswordToAnotherAddress`.

**Why it's right:** the password and key unlock a client that can download and delete anything on
the machine, and `GET /config` answers anyone who reaches the web UI. Not sending them out is the
point. But the web UI and `config set` save the whole config object: without the placeholder, every
save made without retyping the password would erase it. Sending the placeholder back unchanged is
how both keep it, and typing a new value (or clearing the field) still changes it.

Webhook secrets are not masked: the Notifications page edits them in place, and they unlock only a
notification. A bug report redacts all of them anyway.

**Don't "fix" by:** reusing the saved secret for a changed address "so the user doesn't have to retype it"; returning the real password from `GET /config` "because the UI needs it"; saving
the placeholder literally; masking in the frontend only; dropping `KeepSecrets` from the connection
test (testing unsaved settings would then always fail with the saved password).

**Amendment (see [#147](147-episodes-already-in-shoko-leave-the-list-both.md)):** the same rule covers a third secret, `shoko_api_key`, the key
Shoko returns at sign-in. `MaskSecrets` masks it, and `KeepSecrets` keeps it only for the same
`shoko_url`; another address gets `ErrShokoNeedsSignIn`, since the password isn't kept and can't
be retyped, so the user signs in again. Bug reports redact it (`diagnostics/bundle.go`). Pinned by
`TestShokoKeyIsMaskedAndKeptOnlyForTheSameAddress` and `TestBuildRedactsTheQBittorrentCredentials`.
