package daemon

import (
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/torrents"
)

// QBitSettingsFrom is the qBittorrent part of the config, as the torrents package takes it: torrents
// doesn't know files.Config (tests/unit/architecture_test.go).
func QBitSettingsFrom(c *files.Config) torrents.QBitSettings {
	out := torrents.QBitSettings{
		URL:      c.QBittorrentURL,
		Username: c.QBittorrentUsername,
		Password: c.QBittorrentPassword,
		APIKey:   c.QBittorrentAPIKey,
		Category: c.QBittorrentCategory,
		// Read on every Ensure, like the rest: turning it on applies on the next pass.
		ManageSettings: c.QBittorrentManageSettings,
		UploadFloorKiB: c.QBittorrentDisableSeedingUploadKiB,
	}
	for _, m := range c.QBittorrentPathMap {
		out.PathMap = append(out.PathMap, torrents.QBitPathMapping{App: m.App, QBittorrent: m.QBittorrent})
	}
	return out
}
