package files

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"sync"
	"time"

	"AutoAnimeDownloader/src/internal/nyaa"
)

// The ways a torrent reaches the client, as a release record says it (ReleaseRecord.Origin).
const (
	// ReleaseOriginSearch: the check pass, or a standalone anime's first download, picked it from a
	// search.
	ReleaseOriginSearch = "search"
	// ReleaseOriginManual: a manual episode download picked it from a search.
	ReleaseOriginManual = "manual"
	// ReleaseOriginPicker: the user picked it in the torrent picker.
	ReleaseOriginPicker = "picker"
	// ReleaseOriginMagnet: the user pasted its magnet.
	ReleaseOriginMagnet = "magnet"
)

// ReleaseRecord is what the app knew about a torrent when it added it: the release, how it came,
// and the tags its name gives (the release-filters spec §9). Keyed by info hash in releases.json:
// a pack is one torrent behind many episode records, so episodes.json is the wrong place
// (decisions/150).
type ReleaseRecord struct {
	Name      string `json:"name"`
	DetailURL string `json:"detail_url,omitempty"`
	// Size, Seeders and Leechers are the Nyaa row's when the release was chosen.
	Size       int64     `json:"size,omitempty"`
	Seeders    int       `json:"seeders,omitempty"`
	Leechers   int       `json:"leechers,omitempty"`
	UploadedAt time.Time `json:"uploaded_at,omitzero"`
	Trusted    bool      `json:"trusted,omitempty"`
	Remake     bool      `json:"remake,omitempty"`
	AddedAt    time.Time `json:"added_at"`
	Origin     string    `json:"origin"`
	// Release is the name parsed once, at add time (nyaa.ParseRelease).
	Release nyaa.Release `json:"release"`
	// Episodes are the episodes the torrent was added for, by anime id. episodes.json says the same
	// while the torrent lives; this copy is what a removed torrent's card still shows.
	Episodes map[int][]int `json:"episodes,omitempty"`
}

// ReleaseStore is releases.json: one ReleaseRecord per info hash, written at every add.
type ReleaseStore struct {
	path string
	mu   sync.Mutex
}

func NewReleaseStore(path string) *ReleaseStore {
	return &ReleaseStore{path: path}
}

// Record saves what the app knows about a torrent it just added. A hash already recorded keeps
// its record: a redownload re-adds the same torrent (decisions/140), and a pack is added once per
// episode it covers. Only the episodes join, so the record lists every episode added from it.
func (s *ReleaseStore) Record(hash string, rec ReleaseRecord, animeID int, episodes ...int) error {
	if s == nil || hash == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.loadLocked()
	if err != nil {
		return err
	}
	if existing, ok := all[hash]; ok {
		rec = existing
	}
	if animeID != 0 && len(episodes) > 0 {
		if rec.Episodes == nil {
			rec.Episodes = map[int][]int{}
		}
		for _, ep := range episodes {
			if !slices.Contains(rec.Episodes[animeID], ep) {
				rec.Episodes[animeID] = append(rec.Episodes[animeID], ep)
			}
		}
		slices.Sort(rec.Episodes[animeID])
	}
	all[hash] = rec
	return s.saveLocked(all)
}

// All returns every record, by info hash. A missing file is an empty store.
func (s *ReleaseStore) All() (map[string]ReleaseRecord, error) {
	if s == nil {
		return map[string]ReleaseRecord{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *ReleaseStore) loadLocked() (map[string]ReleaseRecord, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]ReleaseRecord{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read releases: %w", err)
	}
	all := map[string]ReleaseRecord{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &all); err != nil {
			return nil, fmt.Errorf("parse releases: %w", err)
		}
	}
	return all, nil
}

func (s *ReleaseStore) saveLocked(all map[string]ReleaseRecord) error {
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write releases: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("write releases: %w", err)
	}
	return nil
}
