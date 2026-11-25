package daemon

import (
	"sync/atomic"
	"time"

	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/nyaa"
)

// ReleasesFileName is the release store's file in the data dir.
const ReleasesFileName = "releases.json"

// releaseStore is releases.json, set once at start (SetReleaseStore). It is package state, not a
// field of State, because the add paths sit deep in the pass (attemptDownloadWithRetries) and in
// the API's manual downloads, and none of them carries State. Unset (tests, debug runs), recording
// does nothing (decisions/150).
var releaseStore atomic.Pointer[files.ReleaseStore]

// SetReleaseStore is called once by the daemon's main.
func SetReleaseStore(s *files.ReleaseStore) { releaseStore.Store(s) }

// ReleaseStore is the store the API reads the anime page's torrent cards from; nil when unset.
func ReleaseStore() *files.ReleaseStore { return releaseStore.Load() }

// recordRelease writes what the app knew about a torrent it just added. A failed write is logged,
// never returned: the torrent is in the client, and its card falls back to the client's name.
func recordRelease(hash string, tr nyaa.TorrentResult, origin string, animeID int, episodes ...int) {
	s := releaseStore.Load()
	if s == nil || hash == "" {
		return
	}
	rec := files.ReleaseRecord{
		Name:       tr.Name,
		DetailURL:  tr.DetailURL,
		Size:       tr.Size,
		Seeders:    nyaa.ParseSeeders(tr.Seeders),
		Leechers:   tr.Leechers,
		UploadedAt: tr.Date,
		Trusted:    tr.Trusted,
		Remake:     tr.Remake,
		AddedAt:    time.Now().UTC(),
		Origin:     origin,
		Release:    nyaa.ParseRelease(tr.Name),
	}
	if err := s.Record(hash, rec, animeID, episodes...); err != nil {
		logger.Logger.Warn().Err(err).Str("hash", hash).Msg("Could not record the release details of an added torrent")
	}
}

// PickedRelease is what the torrent picker knows of the release it sends with a magnet: its name,
// Nyaa id and size. A zero NyaaID is a pasted magnet.
type PickedRelease struct {
	Name      string
	NyaaID    int
	SizeBytes int64
}

// record writes the release: a pick, with its Nyaa page, when it came with a Nyaa id; else a pasted
// magnet. The picker and a paste share the same add paths, so the Nyaa id is what tells them apart.
func (p PickedRelease) record(hash, magnet string, animeID int, episodes ...int) {
	if p.NyaaID <= 0 {
		recordMagnet(hash, magnet, files.ReleaseOriginMagnet, animeID, episodes...)
		return
	}
	name := p.Name
	if name == "" {
		name = magnetDisplayName(magnet)
	}
	recordRelease(hash, nyaa.TorrentResult{Name: name, Size: p.SizeBytes, DetailURL: nyaa.ViewURL(p.NyaaID)}, files.ReleaseOriginPicker, animeID, episodes...)
}

// recordMagnet is recordRelease for a magnet the user pasted: its name is the magnet's dn, and
// there is no Nyaa row.
func recordMagnet(hash, magnet, origin string, animeID int, episodes ...int) {
	recordRelease(hash, nyaa.TorrentResult{Name: magnetDisplayName(magnet)}, origin, animeID, episodes...)
}
