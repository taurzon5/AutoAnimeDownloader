package shoko

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// StateFileName is the store's file in the data dir.
const StateFileName = "shoko.json"

// State is what the app knows of a Shoko library, saved between runs.
type State struct {
	// URL is the server the state was read from: a store read from another one starts over.
	URL    string         `json:"url"`
	Series map[int]Series `json:"series"`
	// Owned holds the owned episodes of the series that match an anime the app tracks, by series.
	Owned map[int][]Episode `json:"owned"`
	// NewestImport bounds the next incremental read: files imported at or after it are read.
	// NewestImportIDs are the files already seen at that very instant, skipped on the next read:
	// read again, their series would be refetched on every sync for nothing.
	NewestImport    time.Time `json:"newest_import"`
	NewestImportIDs []int     `json:"newest_import_ids,omitempty"`
	LastFullSync    time.Time `json:"last_full_sync"`
	LastSync        time.Time `json:"last_sync"`
	LastError       string    `json:"last_error,omitempty"`
	// ImportFolders are Shoko's import folders as last read, kept so a torrent that finishes right
	// after a restart, before Shoko is read again, still goes to Shoko (decisions/152).
	ImportFolders []ImportFolder `json:"import_folders,omitempty"`
}

// Store keeps the State in a file and syncs it with Shoko.
type Store struct {
	path string
	// syncMu runs one Sync or RefreshSeries at a time: each reads the state, talks to Shoko, then
	// writes it back, and two at once would overwrite each other.
	syncMu sync.Mutex
	mu     sync.Mutex
	state  State
}

// NewStore loads the store from dataDir, or starts empty.
func NewStore(dataDir string) *Store {
	s := &Store{path: filepath.Join(dataDir, StateFileName)}
	if data, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(data, &s.state)
	}
	if s.state.Series == nil {
		s.state.Series = map[int]Series{}
	}
	if s.state.Owned == nil {
		s.state.Owned = map[int][]Episode{}
	}
	return s
}

// Snapshot returns a copy of the state.
func (s *Store) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.state
	out.Series = make(map[int]Series, len(s.state.Series))
	for k, v := range s.state.Series {
		out.Series[k] = v
	}
	out.Owned = make(map[int][]Episode, len(s.state.Owned))
	for k, v := range s.state.Owned {
		out.Owned[k] = v
	}
	return out
}

// SeriesList returns every known series, by id.
func (s *Store) SeriesList() []Series {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Series, 0, len(s.state.Series))
	for _, v := range s.state.Series {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// HasOwned reports whether the owned episodes of a series were read at all: false for a series no
// sync has read yet (not followed then), true even when it has none.
func (s *Store) HasOwned(seriesID int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.state.Owned[seriesID]
	return ok
}

// SeriesForChange turns a Shoko 6 event's AniDB anime ids and file ids into the Shoko series the
// store knows: a series by its AniDB id, a file by the owned episodes it is a file of. unknown are
// the AniDB ids no known series has (a series the store never read), for the caller to look up.
func (s *Store) SeriesForChange(anidb, files []int) (series, unknown []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[int]bool{}
	add := func(id int) {
		if !seen[id] {
			seen[id] = true
			series = append(series, id)
		}
	}
	for _, a := range anidb {
		found := false
		for _, sr := range s.state.Series {
			if sr.AniDB == a {
				add(sr.ID)
				found = true
			}
		}
		if !found {
			unknown = append(unknown, a)
		}
	}
	for _, f := range files {
		for id, eps := range s.state.Owned {
			for _, ep := range eps {
				for _, file := range ep.Files {
					if file.ID == f {
						add(id)
					}
				}
			}
		}
	}
	sort.Ints(series)
	return series, unknown
}

// OwnedEpisodes returns the owned episodes of a series, as of the last sync.
func (s *Store) OwnedEpisodes(seriesID int) []Episode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Owned[seriesID]
}

// ImportFolders returns Shoko's import folders as last read.
func (s *Store) ImportFolders() []ImportFolder {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ImportFolder(nil), s.state.ImportFolders...)
}

// SetImportFolders keeps Shoko's import folders, as just read, and saves them.
func (s *Store) SetImportFolders(folders []ImportFolder) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.ImportFolders = append([]ImportFolder(nil), folders...)
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Due reports whether a sync should run now: never synced, synced from another server, or the last
// sync is older than every.
func (s *Store) Due(url string, every time.Duration, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.LastFullSync.IsZero() || s.state.URL != url || now.Sub(s.state.LastSync) >= every
}

// FullDue reports whether the last full sync is days old or older; never with days <= 0, or
// before the first full sync (which Due already makes the next sync).
func (s *Store) FullDue(days int, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return days > 0 && !s.state.LastFullSync.IsZero() && now.Sub(s.state.LastFullSync) >= time.Duration(days)*24*time.Hour
}

// Sync brings the state up to date (the Shoko spec §6). The first sync, a sync from another server
// and a full one read every series, then the owned episodes of the series that match a tracked
// MAL id. After that only files imported since the newest one seen are read: their series are read
// again, a series not seen yet is fetched by id, and a tracked series never read is read now.
// tracked holds the MAL ids of the anime the app follows, and bySeries the series a match set by
// hand points at, which may share no MAL id with them. A failure leaves the previous state, with the
// error recorded.
func (s *Store) Sync(ctx context.Context, c *Client, url string, tracked, bySeries map[int]bool, full bool, now time.Time) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	full = full || state.LastFullSync.IsZero() || state.URL != url

	next, err := sync1(ctx, c, state, tracked, bySeries, full)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.state.LastError = err.Error()
		_ = s.saveLocked()
		return err
	}
	next.URL = url
	next.LastSync = now
	if full {
		next.LastFullSync = now
	}
	next.LastError = ""
	// The import folders are read apart (SetImportFolders), maybe while this sync ran.
	next.ImportFolders = s.state.ImportFolders
	s.state = next
	return s.saveLocked()
}

// RefreshSeries re-reads the series a change Shoko reported names (Watch): a series not known yet is
// fetched, and the owned episodes of those that match a followed anime (tracked MAL ids) or a match
// set by hand (bySeries) are read again. A deletion is seen this way, which the newer-only refresh
// can't. Before the first full sync (or for another server) it does nothing: the next pass reads
// everything anyway.
func (s *Store) RefreshSeries(ctx context.Context, c *Client, url string, ids []int, tracked, bySeries map[int]bool) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	if state.LastFullSync.IsZero() || state.URL != url {
		return nil
	}
	// One series failing doesn't stop the others: a series Shoko no longer has (404) is dropped, and
	// any other error is returned once every series was tried.
	series := map[int]Series{}
	owned := map[int][]Episode{}
	var gone []int
	var firstErr error
	for _, id := range ids {
		sr, known := state.Series[id]
		var err error
		if !known {
			sr, err = c.SeriesByID(ctx, id)
		}
		var eps []Episode
		if err == nil && (tracksAny(sr, tracked) || bySeries[id]) {
			eps, err = c.OwnedEpisodes(ctx, id)
			if err == nil {
				owned[id] = eps
			}
		}
		switch {
		case errors.Is(err, ErrNotFound):
			gone = append(gone, id)
		case err != nil:
			if firstErr == nil {
				firstErr = err
			}
		default:
			series[id] = sr
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sr := range series {
		s.state.Series[id] = sr
	}
	for id, eps := range owned {
		s.state.Owned[id] = eps
	}
	for _, id := range gone {
		delete(s.state.Series, id)
		delete(s.state.Owned, id)
	}
	if err := s.saveLocked(); err != nil {
		return err
	}
	return firstErr
}

func sync1(ctx context.Context, c *Client, prev State, tracked, bySeries map[int]bool, full bool) (State, error) {
	next := State{Series: map[int]Series{}, Owned: map[int][]Episode{}, NewestImport: prev.NewestImport,
		NewestImportIDs: prev.NewestImportIDs, LastFullSync: prev.LastFullSync}
	refresh := map[int]bool{}
	if full {
		all, err := c.AllSeries(ctx)
		if err != nil {
			return State{}, err
		}
		for _, sr := range all {
			next.Series[sr.ID] = sr
			refresh[sr.ID] = true
		}
		newest, id, err := c.NewestImport(ctx)
		if err != nil {
			return State{}, err
		}
		next.NewestImport, next.NewestImportIDs = newest, nil
		if id != 0 {
			next.NewestImportIDs = []int{id}
		}
	} else {
		for id, sr := range prev.Series {
			next.Series[id] = sr
		}
		for id, eps := range prev.Owned {
			next.Owned[id] = eps
		}
		files, err := c.FilesImportedSince(ctx, prev.NewestImport)
		if err != nil {
			return State{}, err
		}
		seen := map[int]bool{}
		for _, id := range prev.NewestImportIDs {
			seen[id] = true
		}
		for _, f := range files {
			if f.Imported.Equal(prev.NewestImport) && seen[f.ID] {
				continue
			}
			switch {
			case f.Imported.After(next.NewestImport):
				next.NewestImport, next.NewestImportIDs = f.Imported, []int{f.ID}
			case f.Imported.Equal(next.NewestImport):
				next.NewestImportIDs = append(next.NewestImportIDs, f.ID)
			}
			for _, id := range f.Series {
				refresh[id] = true
				if _, known := next.Series[id]; !known {
					sr, err := c.SeriesByID(ctx, id)
					if err != nil {
						return State{}, err
					}
					next.Series[id] = sr
				}
			}
		}
	}

	for id, sr := range next.Series {
		if !tracksAny(sr, tracked) && !bySeries[id] {
			delete(next.Owned, id)
			continue
		}
		if _, read := next.Owned[id]; read && !refresh[id] {
			continue
		}
		owned, err := c.OwnedEpisodes(ctx, id)
		if err != nil {
			return State{}, err
		}
		next.Owned[id] = owned
	}
	return next, nil
}

func tracksAny(sr Series, tracked map[int]bool) bool {
	for _, m := range sr.MAL {
		if tracked[m] {
			return true
		}
	}
	return false
}
