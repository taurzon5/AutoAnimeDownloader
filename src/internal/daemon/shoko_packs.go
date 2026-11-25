package daemon

import (
	"errors"
	"io/fs"
	"os"
	"slices"

	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/nyaa"
	"AutoAnimeDownloader/src/internal/shoko"
	"AutoAnimeDownloader/src/internal/torrents"
)

// dropShokoPackFiles is "remove the unneeded episodes" (decisions/153). A pack chosen for the
// missing episodes also brings the ones the user has in Shoko. For an anime that doesn't keep them
// (Config.KeepsShokoPackEpisodes), their files are skipped, and deleted once the torrent finished:
//
//   - qBittorrent never downloads a skipped file (priority 0) and keeps seeding the rest after one is
//     deleted (measured on 5.x).
//   - rain downloads every file and can't seed a torrent with one missing, so a finished rain torrent
//     with files to delete is released first: it leaves the client, the rest of its files kept.
//
// Only episodes the app has no record of count: a record is the app's own copy (decisions/147).
// Only files a pack of this anime maps to one of its episodes are touched (nyaa.FilesInPackWindow,
// which needs the anime's episode total): an extra, another cour's file or an unmapped name stays.
// Direct mode only: a hardlink-mode pack has no placement to say which file is which. A pack
// handed to Shoko, or in one of Shoko's import folders (folders), is only skipped, never deleted:
// what was written there is Shoko's (decisions/152).
func dropShokoPackFiles(configs *files.Config, backend torrents.TorrentBackend, fm FileManagerInterface, folders ShokoImporter, saved []files.EpisodeStruct, inShoko map[int]map[int]shoko.File, settings map[int]files.AnimeSettings) {
	if backend == nil || len(inShoko) == 0 {
		return
	}
	recorded := map[files.EpisodeKey]bool{}
	byHash := map[string][]files.EpisodeStruct{}
	for _, ep := range saved {
		recorded[ep.Key()] = true
		if ep.IsBatch && ep.EpisodeHash != "" {
			byHash[ep.EpisodeHash] = append(byHash[ep.EpisodeHash], ep)
		}
	}
	for hash, records := range byHash {
		p, placed := backend.Placement(hash)
		if !placed || len(p.Files) == 0 {
			continue
		}
		unneeded := unneededPackFiles(configs, p, records, inShoko, settings, recorded)
		if len(unneeded) == 0 {
			continue
		}
		if err := dropPackFiles(configs, backend, fm, folders, hash, p, records, unneeded); err != nil {
			logger.Logger.Warn().Err(err).Str("hash", hash).Msg("Could not remove the episodes in Shoko a pack brought")
		}
	}
}

// unneededPackFiles returns the torrent paths of the pack's files that are episodes the user has in
// Shoko, of an anime that removes them, with no record of the app.
func unneededPackFiles(configs *files.Config, p torrents.Placement, records []files.EpisodeStruct, inShoko map[int]map[int]shoko.File, settings map[int]files.AnimeSettings, recorded map[files.EpisodeKey]bool) []string {
	var videos []string
	for _, f := range p.Files {
		if nyaa.IsVideoFile(f.Path) {
			videos = append(videos, f.Torrent)
		}
	}
	var out []string
	seen := map[int]bool{}
	for _, head := range records {
		if seen[head.AnimeID] {
			continue
		}
		seen[head.AnimeID] = true
		owned := inShoko[head.AnimeID]
		if len(owned) == 0 || configs.KeepsShokoPackEpisodes(settings[head.AnimeID]) {
			continue
		}
		for torrentPath, local := range nyaa.FilesInPackWindow(videos, head.SeriesOffset, head.AnimeTotalEpisodes, head.AnimeSeason) {
			if local <= 0 {
				continue
			}
			if _, inShokoToo := owned[local]; !inShokoToo || recorded[files.EpisodeKey{AnimeID: head.AnimeID, Episode: local}] {
				continue
			}
			out = append(out, torrentPath)
		}
	}
	slices.Sort(out)
	return out
}

// dropPackFiles skips the files and, once the torrent finished, deletes them: a rain torrent still
// in the client is released first. The records' library paths then drop the deleted files.
func dropPackFiles(configs *files.Config, backend torrents.TorrentBackend, fm FileManagerInterface, folders ShokoImporter, hash string, p torrents.Placement, records []files.EpisodeStruct, unneeded []string) error {
	fresh := false
	for _, f := range p.Files {
		if !f.Skipped && slices.Contains(unneeded, f.Torrent) {
			fresh = true
			break
		}
	}
	if fresh {
		if err := backend.SkipPlacedFiles(hash, unneeded); err != nil {
			return err
		}
		logger.Logger.Info().Str("hash", hash).Int("files", len(unneeded)).Msg("Skipping the episodes of this pack already in Shoko")
	}
	p, _ = backend.Placement(hash)
	if !p.Completed {
		return nil // qBittorrent won't write them; rain's are deleted once it finished
	}
	// Downloaded into one of Shoko's import folders: a file written there is Shoko's, which the app
	// never deletes (decisions/152). The folder counts too, not only the records organize marked.
	// The skip alone stands: qBittorrent never writes what it skips.
	if folders != nil && folders.InShoko(configs, p.RootOr(configs.CompletedAnimePath)) {
		return nil
	}
	for _, ep := range records {
		if ep.HandedToShoko {
			return nil
		}
	}
	var toDelete []string
	for _, f := range p.Files {
		if !f.Skipped {
			continue
		}
		if _, err := os.Stat(p.DiskPath(f)); err == nil {
			toDelete = append(toDelete, p.DiskPath(f))
		}
	}
	if len(toDelete) == 0 {
		return nil
	}
	if _, inClient := backend.Get(hash); inClient && !configs.UsesQBittorrent() {
		// rain can't seed a torrent with files missing: it leaves first, the rest kept.
		if !releaseTorrent(hash, backend, fm, records, true, false) {
			return errors.New("could not take the torrent out of the client before deleting its files")
		}
	}
	for _, disk := range toDelete {
		if err := os.Remove(disk); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		logger.Logger.Info().Str("hash", hash).Str("path", disk).Msg("Deleted an episode of a pack that is already in Shoko")
	}
	keep := p.VideoPaths()
	var changed []files.EpisodeStruct
	for _, ep := range records {
		if len(ep.LibraryPaths) == 0 || slices.Equal(ep.LibraryPaths, keep) {
			continue
		}
		ep.LibraryPaths = keep
		changed = append(changed, ep)
	}
	if len(changed) > 0 {
		return fm.UpsertEpisodes(changed)
	}
	return nil
}
