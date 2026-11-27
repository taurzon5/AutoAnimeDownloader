package files

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"unicode"

	"AutoAnimeDownloader/src/internal/nyaa"
)

// QueuesFileName is the queues' store in the data folder. Not in config.json: PUT /config writes
// the whole config the Settings page loaded, so a queue made in another tab would be lost
// (decisions/164).
const QueuesFileName = "queues.json"

// DefaultQueueID is the queue every anime is in until it is moved: the app starts with it and it
// can't be deleted. Its ranking is config.json's priorities (the Priorities page's Default).
const DefaultQueueID = "default"

// DefaultQueueName is the default queue's name until the user renames it.
const DefaultQueueName = "Default"

// DownloadQueue is a queue the user made (decisions/164): its place in the list is its order, and
// it ranks releases with its own priorities.
type DownloadQueue struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Priorities is the queue's ranking; nil on the default queue, which uses config.priorities.
	// The ignore list in it is not used: the ignore list is shared by every queue.
	Priorities *nyaa.Priorities `json:"priorities,omitempty"`
	// Animes are the media ids in the queue. Always empty on the default queue: it holds every anime
	// no other queue lists, the ones from AniList and MyAnimeList lists included.
	Animes []int `json:"animes,omitempty"`
}

// IsDefault reports the default queue.
func (q DownloadQueue) IsDefault() bool { return q.ID == DefaultQueueID }

// QueueOf is the position and queue of mediaID in list: the queue that lists it, else the default.
func QueueOf(list []DownloadQueue, mediaID int) (int, DownloadQueue) {
	def := -1
	for i, q := range list {
		if q.IsDefault() {
			def = i
			continue
		}
		for _, id := range q.Animes {
			if id == mediaID {
				return i, q
			}
		}
	}
	if def < 0 {
		return 0, DownloadQueue{ID: DefaultQueueID, Name: DefaultQueueName}
	}
	return def, list[def]
}

// MoveAnime puts mediaID in queue id (the default queue: in none), and takes it out of any other.
func MoveAnime(list []DownloadQueue, mediaID int, id string) []DownloadQueue {
	out := make([]DownloadQueue, len(list))
	for i, q := range list {
		var kept []int
		for _, a := range q.Animes {
			if a != mediaID {
				kept = append(kept, a)
			}
		}
		if q.ID == id && !q.IsDefault() {
			kept = append(kept, mediaID)
		}
		q.Animes = kept
		out[i] = q
	}
	return out
}

// FindQueue is the queue with id, or false.
func FindQueue(list []DownloadQueue, id string) (DownloadQueue, bool) {
	for _, q := range list {
		if q.ID == id {
			return q, true
		}
	}
	return DownloadQueue{}, false
}

// QueueStore is queues.json. Every change goes through it under one lock.
type QueueStore struct {
	path string
	mu   sync.Mutex
}

// NewQueueStore opens the store at path (created on the first save).
func NewQueueStore(path string) *QueueStore { return &QueueStore{path: path} }

// All is the queues in their order, the default queue always among them (first when the file
// doesn't place it).
func (s *QueueStore) All() ([]DownloadQueue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *QueueStore) loadLocked() ([]DownloadQueue, error) {
	var list []DownloadQueue
	data, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("read queues: %w", err)
	case len(data) > 0:
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, fmt.Errorf("parse queues: %w", err)
		}
	}
	return withDefaultQueue(list), nil
}

func withDefaultQueue(list []DownloadQueue) []DownloadQueue {
	for i := range list {
		if list[i].IsDefault() {
			list[i].Priorities = nil
			list[i].Animes = nil
			return list
		}
	}
	return append([]DownloadQueue{{ID: DefaultQueueID, Name: DefaultQueueName}}, list...)
}

// Update runs change over the saved queues and saves what it returns, unless it returns an error.
func (s *QueueStore) Update(change func([]DownloadQueue) ([]DownloadQueue, error)) ([]DownloadQueue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	next, err := change(list)
	if err != nil {
		return nil, err
	}
	next = withDefaultQueue(next)
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return nil, err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return nil, fmt.Errorf("write queues: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return nil, fmt.Errorf("write queues: %w", err)
	}
	return next, nil
}

// QueueID makes a stable id from a name: lowercase letters, digits and dashes, made unique
// against taken. Renaming keeps the id, so the anime in the queue keep pointing at it.
func QueueID(name string, taken func(string) bool) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" || base == DefaultQueueID {
		base = "queue"
	}
	id := base
	for n := 2; taken(id); n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}
