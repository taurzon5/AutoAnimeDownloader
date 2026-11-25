package daemon

import (
	"context"
	"time"

	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/shoko"
	"AutoAnimeDownloader/src/internal/torrents"
)

// Download folders that are Shoko's import folders (decisions/152): the app reads Shoko's import
// folders and never manages them. Organize marks each torrent's records handed to Shoko when its
// folder is one, and the torrent keeps seeding; then the job queue calls RequestImport, so Shoko
// imports the files now instead of whenever its folder watcher, which may be off, sees them. A
// torrent whose files Shoko moved leaves the client (releaseMovedByShoko).

// ShokoFolder returns the import folder of Shoko that holds the download root at root: root, as
// Shoko sees it through shoko_path_map, is that folder or lies inside it (the deepest one wins).
// Only with Shoko on and in direct mode, from the folders as last read.
func (s *ShokoService) ShokoFolder(cfg *files.Config, root string) (shoko.ImportFolder, bool) {
	if s == nil || !cfg.ShokoEnabled || !cfg.IsDirectMode() || root == "" {
		return shoko.ImportFolder{}, false
	}
	return folderHolding(s.store.ImportFolders(), cfg.ShokoPath(root))
}

// InShoko reports whether the download root at root is one of Shoko's import folders.
func (s *ShokoService) InShoko(cfg *files.Config, root string) bool {
	_, ok := s.ShokoFolder(cfg, root)
	return ok
}

func folderHolding(folders []shoko.ImportFolder, shokoPath string) (shoko.ImportFolder, bool) {
	var best shoko.ImportFolder
	found := false
	for _, f := range folders {
		if files.UnderFolder(shokoPath, f.Path) && (!found || len(f.Path) > len(best.Path)) {
			best, found = f, true
		}
	}
	return best, found
}

// RequestImport asks Shoko to import new files once the downloads that finish close together are
// in (importSettle): one request for them all. The job queue calls it only for a torrent in one of
// Shoko's folders. It never blocks, since organize must not wait for Shoko. A failed request is
// kept, and the next pass sends it again (retryImport).
func (s *ShokoService) RequestImport(cfg *files.Config) {
	if s == nil || !cfg.ShokoEnabled || !cfg.IsDirectMode() || cfg.ShokoURL == "" || cfg.ShokoAPIKey == "" {
		return
	}
	s.mu.Lock()
	s.importWanted = true
	if s.importRunning {
		s.mu.Unlock()
		return
	}
	s.importRunning = true
	settle := s.importSettle
	s.mu.Unlock()
	go s.sendImports(cfg.ShokoURL, cfg.ShokoAPIKey, settle)
}

// sendImports sends one request per settle window while requests keep coming, then stops.
func (s *ShokoService) sendImports(url, key string, settle time.Duration) {
	for {
		time.Sleep(settle)
		s.mu.Lock()
		if !s.importWanted {
			s.importRunning = false
			s.mu.Unlock()
			return
		}
		s.importWanted = false
		s.mu.Unlock()

		err := importNewFiles(url, key)
		s.mu.Lock()
		s.lastImport = time.Now()
		s.importRetry = err != nil
		s.importError = ""
		if err != nil {
			s.importError = err.Error()
		}
		s.mu.Unlock()
		if err != nil {
			logger.Logger.Warn().Err(err).Msg("Shoko: could not ask it to import the new files; the next pass asks again")
		} else {
			logger.Logger.Info().Msg("Asked Shoko to import the new files in its folders")
		}
	}
}

func importNewFiles(url, key string) error {
	client, err := shoko.NewClient(url, key)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return client.ImportNewFiles(ctx)
}

// retryImport is the pass's half: an import request that failed is sent again.
func (s *ShokoService) retryImport(cfg *files.Config) {
	s.mu.Lock()
	retry := s.importRetry
	s.mu.Unlock()
	if retry {
		s.RequestImport(cfg)
	}
}

// readFolders reads Shoko's import folders, with the sync (refresh) or once a run, and keeps them
// in the store. A failure keeps the last list and says why.
func (s *ShokoService) readFolders(ctx context.Context, cfg *files.Config, refresh bool) {
	s.mu.Lock()
	read := s.foldersRead
	s.mu.Unlock()
	if read && !refresh {
		return
	}
	client, err := shoko.NewClient(cfg.ShokoURL, cfg.ShokoAPIKey)
	var folders []shoko.ImportFolder
	if err == nil {
		folders, err = client.ImportFolders(ctx)
	}
	if err == nil {
		err = s.store.SetImportFolders(folders)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.foldersError = err.Error()
		logger.Logger.Warn().Err(err).Msg("Shoko: could not read its import folders")
		return
	}
	s.foldersRead, s.foldersError = true, ""
}

// ImportStatus is the import side of GET /shoko/status: the last import request (its time and
// error) and Shoko's import folders as last read.
type ImportStatus struct {
	LastRequest  time.Time
	Error        string
	Folders      []shoko.ImportFolder
	FoldersError string
}

// ImportState is the import side's state, for GET /shoko/status.
func (s *ShokoService) ImportState() ImportStatus {
	if s == nil {
		return ImportStatus{}
	}
	folders := s.store.ImportFolders()
	s.mu.Lock()
	defer s.mu.Unlock()
	return ImportStatus{LastRequest: s.lastImport, Error: s.importError, Folders: folders, FoldersError: s.foldersError}
}

// releaseMovedByShoko takes out of the client each torrent handed to Shoko whose files are gone from
// where it placed them while Shoko has one of its episodes. Shoko moved them on import (its renamer's
// MoveOnImport), so the torrent can't seed them, and left alone it would sit in Downloads as "File
// not found", offering a redownload that would only bring Shoko a duplicate. It leaves with what is
// left kept, its records stay, released, and its placement is forgotten: the files are Shoko's.
// A file missing while Shoko has none of the torrent's episodes was deleted by the user: decisions/139
// as for any torrent (decisions/152).
func releaseMovedByShoko(backend torrents.TorrentBackend, fm FileManagerInterface, saved []files.EpisodeStruct, inShoko map[int]map[int]shoko.File) {
	if backend == nil || len(inShoko) == 0 {
		return
	}
	handed := map[string]bool{}
	for _, ep := range saved {
		if ep.HandedToShoko && !ep.TorrentReleased && ep.EpisodeHash != "" {
			handed[ep.EpisodeHash] = true
		}
	}
	for hash := range handed {
		p, placed := backend.Placement(hash)
		if !placed || !PlacedFilesMissing(p) {
			continue
		}
		if _, inSession := backend.Get(hash); !inSession {
			continue
		}
		var records []files.EpisodeStruct
		imported := false
		for _, ep := range saved {
			if ep.EpisodeHash != hash {
				continue
			}
			records = append(records, ep)
			if _, ok := inShoko[ep.AnimeID][ep.EpisodeNumber]; ok {
				imported = true
			}
		}
		if !imported {
			continue
		}
		if !releaseTorrent(hash, backend, fm, records, true, true) {
			continue
		}
		if err := backend.ForgetPlacement(hash); err != nil {
			logger.Logger.Warn().Err(err).Str("hash", hash).Msg("Failed to forget the placement of a torrent Shoko moved")
		}
	}
}
