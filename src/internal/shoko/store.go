package shoko

import (
	"context"
	"encoding/json"
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
}

// Store keeps the State in a file and syncs it with Shoko.
type Store struct {
	path  string
	mu    sync.Mutex
	state State
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

// OwnedEpisodes returns the owned episodes of a series, as of the last sync.
func (s *Store) OwnedEpisodes(seriesID int) []Episode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Owned[seriesID]
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

// Sync brings the state up to date (the Shoko spec §6). The first sync, a sync from another server
// and a full one read every series, then the owned episodes of the series that match a tracked
// MAL id. After that only files imported since the newest one seen are read: their series are read
// again, a series not seen yet is fetched by id, and a tracked series never read is read now.
// tracked holds the MAL ids of the anime the app follows, and bySeries the series a match set by
// hand points at, which may share no MAL id with them. A failure leaves the previous state, with the
// error recorded.
func (s *Store) Sync(ctx context.Context, c *Client, url string, tracked, bySeries map[int]bool, full bool, now time.Time) error {
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
	s.state = next
	return s.saveLocked()
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
