package torrents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"AutoAnimeDownloader/src/internal/logger"
	"AutoAnimeDownloader/src/internal/nyaa"

	"github.com/cenkalti/rain/v2/storage"
)

// Direct library mode writes each torrent straight to its final library path and seeds it from
// there: no download folder, no hardlink. rain's built-in file storage can only put a torrent
// under <DataDir>/<id>, so direct mode plugs a storage.Provider into rain (Config.CustomStorage)
// that maps every file of a torrent to a path chosen BEFORE the torrent is added.
//
// The map is frozen the first time rain prepares the torrent (Prepare, after the metadata
// arrives) and saved in placements.json next to the resume database. After that nothing
// recomputes it: a restart, a renamed anime, a changed setting or an AniList correction never
// moves a file that is seeding (decisions/136). Every stored path is RELATIVE to the library root
// (completed_anime_path) and resolved against the current root on use, so a library the user
// moves and re-points the config at keeps seeding instead of downloading everything again
// (decisions/034).

// Placement kinds: where a torrent's files go under its series folder.
const (
	// PlacementEpisode is a single-episode torrent: <series>/Season NN/<file>.
	PlacementEpisode = "episode"
	// PlacementMovie is a movie: <series>/Specials/<file>.
	PlacementMovie = "movie"
	// PlacementPack is any multi-episode torrent: <series>/<the torrent's own layout>.
	PlacementPack = "pack"
)

// placementsFileName is where the placements live, next to session.db, for the same reason as
// queue.json: it is torrent-client state and must follow the resume database.
const placementsFileName = "placements.json"

// partSuffix marks a file that is still downloading when PlacementRequest.PartFiles is set. A
// media server skips it: ".part" is no video extension.
const partSuffix = ".part"

// PlacementRequest is what the daemon decides about a torrent before adding it.
type PlacementRequest struct {
	// Hash is the torrent's info hash (rain's torrent id).
	Hash string `json:"hash"`
	// SeriesDir is the <Series> folder every file of the torrent goes under, relative to the
	// library root ("Frieren"). Placement.Resolve makes it absolute.
	SeriesDir string `json:"series_dir"`
	// SeriesName and SeriesID are the series folder's title and AniList id (the PREQUEL chain's
	// root), kept for its tvshow.nfo: organize never calls AniList (decisions/068).
	SeriesName string `json:"series_name,omitempty"`
	SeriesID   int    `json:"series_id,omitempty"`
	// Kind is PlacementEpisode, PlacementMovie or PlacementPack.
	Kind string `json:"kind"`
	// Season is the season number for PlacementEpisode (the Season NN folder). >= 1.
	Season int `json:"season,omitempty"`
	// RenameVideo, when set on a PlacementEpisode, is the new base name (no extension) of the
	// torrent's video file, e.g. "Frieren - S01E05". Only applied when the torrent holds exactly
	// one video file; everything else keeps its raw name.
	RenameVideo string `json:"rename_video,omitempty"`
	// PartFiles writes "<name>.part" until FinalizePlacement renames the files.
	PartFiles bool `json:"part_files,omitempty"`
}

// PlacedFile is one torrent file and where it lives.
type PlacedFile struct {
	// Torrent is the path rain uses: "<torrent name>/<path inside>" for a multi-file torrent.
	Torrent string `json:"torrent"`
	// Path is the final path, relative to the library root ("Frieren/Season 01/ep05.mkv") as
	// stored, absolute once the placement went through Resolve.
	Path string `json:"path"`
}

// Placement is a PlacementRequest plus the frozen file map.
type Placement struct {
	PlacementRequest
	// Files is empty until rain prepares the torrent (its metadata arrived).
	Files []PlacedFile `json:"files,omitempty"`
	// Completed is set by FinalizePlacement once every .part file got its final name. Without
	// PartFiles it is never needed: files have their final names from the start.
	Completed bool `json:"completed,omitempty"`
	// CreatedAt is when the placement was registered. The daemon's orphan sweep leaves young
	// placements alone: between SetPlacement and the add, a placement has no torrent yet.
	CreatedAt time.Time `json:"created_at,omitempty"`
	// Kept marks a placement whose torrent was removed with keepData on purpose (a manual delete
	// with keep_data). It is never cleared and the placement is never forgotten, and it guards two
	// paths only: the daemon's orphan sweep, and a removal before the metadata arrived — neither
	// deletes its files. Re-adding the same hash resumes into them; from then on the torrent follows
	// the usual removal rules, like any other.
	Kept bool `json:"kept,omitempty"`
}

// Resolve returns the placement with SeriesDir and every file path joined to root. Everything
// that touches the disk works on a resolved placement; the store keeps the relative one. An empty
// root leaves the paths relative.
func (p Placement) Resolve(root string) Placement {
	if root == "" {
		return p
	}
	out := p
	out.SeriesDir = filepath.Join(root, p.SeriesDir)
	out.Files = make([]PlacedFile, len(p.Files))
	for i, f := range p.Files {
		out.Files[i] = PlacedFile{Torrent: f.Torrent, Path: filepath.Join(root, f.Path)}
	}
	return out
}

// DiskPath is where a placed file is on disk right now: its .part name while the placement
// writes part files and has not been finalized.
func (p Placement) DiskPath(f PlacedFile) string {
	if p.PartFiles && !p.Completed {
		return f.Path + partSuffix
	}
	return f.Path
}

// VideoPaths returns the final paths of the placement's video files, sorted. These are what a
// direct-mode torrent records as its library paths.
func (p Placement) VideoPaths() []string {
	var out []string
	for _, f := range p.Files {
		if nyaa.IsVideoFile(f.Path) {
			out = append(out, f.Path)
		}
	}
	sort.Strings(out)
	return out
}

// placementStore persists placements by hash. Every method takes mu; the file is rewritten
// whole on every change (a few KB) through a temp file and a rename, so a crash never leaves a
// half-written map — losing the map would leave rain unable to find a seeding torrent's files.
type placementStore struct {
	mu   sync.Mutex
	path string
	byID map[string]Placement
	// planMu serializes planning: between "is this destination free?" and saving the map, a second
	// torrent preparing at the same moment could pick the same free path. Held across a whole
	// Prepare; mu is still taken inside for each read and write.
	planMu sync.Mutex
}

func newPlacementStore(path string) *placementStore {
	s := &placementStore{path: path, byID: map[string]Placement{}}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		logger.Logger.Warn().Err(err).Str("path", path).Msg("Failed to read torrent placements")
	default:
		if err := json.Unmarshal(data, &s.byID); err != nil {
			logger.Logger.Warn().Err(err).Str("path", path).Msg("Torrent placements are corrupt; direct-mode torrents will not find their files")
			s.byID = map[string]Placement{}
		}
	}
	return s
}

// all returns every placement, unresolved.
func (s *placementStore) all() []Placement {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Placement, 0, len(s.byID))
	for _, p := range s.byID {
		out = append(out, p)
	}
	return out
}

func (s *placementStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byID)
}

func (s *placementStore) get(hash string) (Placement, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[hash]
	return p, ok
}

// register stores a new placement. A hash that already has one keeps it: the map is frozen, and
// re-adding a torrent (a retry after a failure, the pass picking the same pack again) must
// resume into the files it already has instead of planning new ones.
func (s *placementStore) register(req PlacementRequest) (Placement, error) {
	if req.Hash == "" || !filepath.IsLocal(req.SeriesDir) {
		return Placement{}, fmt.Errorf("placement for %q needs a hash and a series folder relative to the library", req.Hash)
	}
	switch req.Kind {
	case PlacementEpisode, PlacementMovie, PlacementPack:
	default:
		return Placement{}, fmt.Errorf("unknown placement kind %q", req.Kind)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.byID[req.Hash]; ok {
		// Kept stays Kept: it is never cleared (decisions/137). CreatedAt is refreshed: the
		// sweep's grace covers the gap between this registration and the add, for a re-add too.
		existing.CreatedAt = time.Now()
		s.byID[req.Hash] = existing
		if err := s.saveLocked(); err != nil {
			return Placement{}, err
		}
		return existing, nil
	}
	p := Placement{PlacementRequest: req, CreatedAt: time.Now()}
	s.byID[req.Hash] = p
	if err := s.saveLocked(); err != nil {
		delete(s.byID, req.Hash)
		return Placement{}, err
	}
	return p, nil
}

func (s *placementStore) update(p Placement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.byID[p.Hash]
	s.byID[p.Hash] = p
	if err := s.saveLocked(); err != nil {
		if had {
			s.byID[p.Hash] = prev
		} else {
			delete(s.byID, p.Hash)
		}
		return err
	}
	return nil
}

func (s *placementStore) forget(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[hash]; !ok {
		return nil
	}
	prev := s.byID[hash]
	delete(s.byID, hash)
	if err := s.saveLocked(); err != nil {
		s.byID[hash] = prev
		return err
	}
	return nil
}

// setKept sets a placement's Kept flag (see Placement.Kept). A hash with no placement is fine: a
// torrent added in hardlink mode has nothing to keep.
func (s *placementStore) setKept(hash string, kept bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[hash]
	if !ok || p.Kept == kept {
		return nil
	}
	p.Kept = kept
	s.byID[hash] = p
	if err := s.saveLocked(); err != nil {
		p.Kept = !kept
		s.byID[hash] = p
		return err
	}
	return nil
}

// ownerOf returns the hash of the placement that claims path (final or .part name), or "".
func (s *placementStore) ownerOf(path string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for hash, p := range s.byID {
		for _, f := range p.Files {
			if f.Path == path || f.Path+partSuffix == path {
				return hash
			}
		}
	}
	return ""
}

func (s *placementStore) saveLocked() error {
	if s.path == "" {
		return nil // tests that never persist
	}
	data, err := json.MarshalIndent(s.byID, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// planFiles computes the frozen file map of a placement from the torrent's file list (rain's
// paths, in torrent order), as paths relative to the library root. It is pure apart from exists,
// which reports whether a (relative) path is already on disk; taken is the set of paths other
// placements claim.
//
// A destination already on disk that this placement doesn't own, or claimed by another
// placement, gets the torrent's short hash appended to its name. Opening it as-is would make
// rain treat someone else's file as this torrent's data and overwrite the pieces that don't
// match.
func planFiles(req PlacementRequest, torrentPaths []string, exists func(string) bool, taken func(string) bool) ([]PlacedFile, error) {
	videos := 0
	for _, tp := range torrentPaths {
		if nyaa.IsVideoFile(tp) {
			videos++
		}
	}

	used := map[string]bool{}
	out := make([]PlacedFile, 0, len(torrentPaths))
	for _, tp := range torrentPaths {
		clean := filepath.Clean(filepath.FromSlash(tp))
		if !filepath.IsLocal(clean) {
			return nil, fmt.Errorf("torrent file path %q leaves the torrent", tp)
		}

		var rel string
		switch req.Kind {
		case PlacementPack:
			rel = clean
		case PlacementMovie:
			rel = filepath.Join("Specials", flatName(clean))
		default:
			season := req.Season
			if season < 1 {
				season = 1
			}
			name := flatName(clean)
			if req.RenameVideo != "" && videos == 1 && nyaa.IsVideoFile(clean) {
				name = req.RenameVideo + filepath.Ext(clean)
			}
			rel = filepath.Join(fmt.Sprintf("Season %02d", season), name)
		}

		dest := filepath.Join(req.SeriesDir, rel)
		if used[dest] {
			// Two files of one torrent flattened onto the same name (an episode torrent with a
			// folder per release): the path inside the torrent is unique.
			dest = filepath.Join(filepath.Dir(dest), strings.ReplaceAll(clean, string(filepath.Separator), " - "))
		}
		if taken(dest) || exists(dest) || exists(dest+partSuffix) {
			dest = withSuffix(dest, shortHash(req.Hash))
		}
		if used[dest] {
			return nil, fmt.Errorf("two files of torrent %s map to %s", req.Hash, dest)
		}
		used[dest] = true
		out = append(out, PlacedFile{Torrent: tp, Path: dest})
	}
	return out, nil
}

// flatName drops the folders of a torrent path: a single episode or a movie lands directly in
// its Season / Specials folder. The torrent name folder rain prepends is dropped with them.
func flatName(clean string) string {
	return filepath.Base(clean)
}

func withSuffix(path, suffix string) string {
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + " [" + suffix + "]" + ext
}

func shortHash(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// placementProvider is the storage.Provider direct mode installs in rain. A torrent with a
// placement gets placedStorage; one without (added in hardlink mode, before the switch) keeps
// the layout rain's own file storage gives it, <fallbackDir>/<id>, so switching modes never
// strands a torrent that is already seeding.
type placementProvider struct {
	store *placementStore
	// root is the library root placements are relative to.
	root        string
	fallbackDir string
	perm        os.FileMode
}

var _ storage.Provider = (*placementProvider)(nil)

func (pp *placementProvider) GetStorage(torrentID string) (storage.Storage, error) {
	if _, ok := pp.store.get(torrentID); ok {
		return &placedStorage{store: pp.store, hash: torrentID, root: pp.root, perm: pp.perm}, nil
	}
	if pp.fallbackDir == "" {
		return nil, fmt.Errorf("torrent %s has no placement and there is no download folder to fall back to", torrentID)
	}
	return &legacyStorage{dest: filepath.Join(pp.fallbackDir, torrentID), perm: pp.perm}, nil
}

// placedStorage serves one placed torrent.
type placedStorage struct {
	store *placementStore
	hash  string
	root  string
	perm  os.FileMode
}

var (
	_ storage.Storage  = (*placedStorage)(nil)
	_ storage.Preparer = (*placedStorage)(nil)
	_ storage.Remover  = (*placedStorage)(nil)
)

// Prepare freezes the file map the first time; later calls (rain calls it before every
// allocation) only check that the torrent still has the files the map was made for.
func (ps *placedStorage) Prepare(_ context.Context, m storage.Manifest) error {
	ps.store.planMu.Lock()
	defer ps.store.planMu.Unlock()
	p, ok := ps.store.get(ps.hash)
	if !ok {
		return fmt.Errorf("placement for %s disappeared", ps.hash)
	}
	var paths []string
	for _, f := range m.Files {
		if !f.Padding {
			paths = append(paths, f.Path)
		}
	}
	if len(p.Files) > 0 {
		if len(p.Files) != len(paths) {
			return fmt.Errorf("torrent %s has %d files but its placement maps %d", ps.hash, len(paths), len(p.Files))
		}
		return nil
	}
	exists := func(rel string) bool { return pathExists(filepath.Join(ps.root, rel)) }
	files, err := planFiles(p.PlacementRequest, paths, exists, func(rel string) bool {
		owner := ps.store.ownerOf(rel)
		return owner != "" && owner != ps.hash
	})
	if err != nil {
		return err
	}
	p.Files = files
	if err := ps.store.update(p); err != nil {
		return fmt.Errorf("failed to save the placement of %s: %w", ps.hash, err)
	}
	logger.Logger.Info().Str("hash", ps.hash).Str("series_dir", p.SeriesDir).Int("files", len(files)).
		Msg("Placed torrent files in the library")
	return nil
}

func (ps *placedStorage) Open(name string, size int64) (storage.File, bool, error) {
	stored, ok := ps.store.get(ps.hash)
	if !ok {
		return nil, false, fmt.Errorf("placement for %s disappeared", ps.hash)
	}
	p := stored.Resolve(ps.root)
	for _, f := range p.Files {
		if f.Torrent != name {
			continue
		}
		path := p.DiskPath(f)
		// A crash between FinalizePlacement's rename and its save leaves the final name on disk
		// with the placement still "not completed". Opening the missing .part would create an
		// empty file: rain sees a missing file, re-checks the whole torrent and downloads that file
		// again, while the finished data sits right there under its final name.
		if path != f.Path && !pathExists(path) && pathExists(f.Path) {
			path = f.Path
		}
		return openFile(path, size, ps.perm)
	}
	return nil, false, fmt.Errorf("torrent %s has no placed file for %q", ps.hash, name)
}

// RootDir is the series folder: TorrentInfo.DataDir means "where this torrent's files are".
func (ps *placedStorage) RootDir() string {
	p, _ := ps.store.get(ps.hash)
	return filepath.Join(ps.root, p.SeriesDir)
}

// Remove deletes the placed files and the folders that became empty, up to the series folder's
// parent (the library root). With keepData the files stay and so does the placement: a torrent
// dropped for a failure resumes into them when the same hash is re-added, and one removed by hand
// with keep_data was marked Kept by the daemon first (decisions/137).
func (ps *placedStorage) Remove(m *storage.Manifest, keepData bool) error {
	if keepData {
		return nil
	}
	p, ok := ps.store.get(ps.hash)
	if !ok {
		return nil
	}
	// rain passes no manifest when the metadata never arrived in this run: this torrent instance
	// wrote nothing. For a Kept placement the files are the user's (a re-added hash that stalled
	// before its metadata, decisions/024), so nothing is deleted. A placement that is not Kept
	// holds the daemon's own partial data and is cleaned up as usual.
	if m == nil && p.Kept {
		return nil
	}
	if err := RemovePlacedFiles(p.Resolve(ps.root)); err != nil {
		return err
	}
	return ps.store.forget(ps.hash)
}

// RemovePlacedFiles deletes a RESOLVED placement's files (final and .part names), the episode
// `.nfo` sidecar Jellyfin writes next to a video (the same sidecar Librarian.RemoveFromLibrary
// removes), and prunes the folders left empty, stopping at the library root (the series folder's
// parent). Missing files are fine.
func RemovePlacedFiles(p Placement) error {
	// An unresolved placement (no session has opened yet) has relative paths, which would resolve
	// against the process's working directory.
	if !filepath.IsAbs(p.SeriesDir) {
		return fmt.Errorf("placement %s is not resolved against a library yet", p.Hash)
	}
	stopAt := filepath.Dir(p.SeriesDir)
	dirs := map[string]bool{}
	for _, f := range p.Files {
		paths := []string{f.Path, f.Path + partSuffix}
		if nyaa.IsVideoFile(f.Path) {
			paths = append(paths, strings.TrimSuffix(f.Path, filepath.Ext(f.Path))+".nfo")
		}
		for _, path := range paths {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("failed to delete %s: %w", path, err)
			}
		}
		dirs[filepath.Dir(f.Path)] = true
	}
	for dir := range dirs {
		pruneEmptyDirs(dir, stopAt)
	}
	return nil
}

// pruneEmptyDirs removes dir and its parents while they hold nothing but Jellyfin leftovers,
// never stopAt or above it. Leftovers are the library sweep's rule (nyaa.IsLeftoverMetadataOnly:
// `.nfo` files and a `.chapters` folder, decisions/106 and decisions/110): they describe files
// that are gone, and left alone they would keep Season NN/ and the series folder forever.
func pruneEmptyDirs(dir, stopAt string) {
	for {
		rel, err := filepath.Rel(stopAt, dir)
		if err != nil || rel == "." || !filepath.IsLocal(rel) {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil || !nyaa.IsLeftoverMetadataOnly(entries) {
			return
		}
		for _, e := range entries {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// legacyStorage is rain's own file storage layout (<DataDir>/<id>, owned by the torrent),
// reimplemented because rain keeps it in an internal package. It serves torrents that have no
// placement while direct mode is on.
type legacyStorage struct {
	dest string
	perm os.FileMode
}

var (
	_ storage.Storage = (*legacyStorage)(nil)
	_ storage.Remover = (*legacyStorage)(nil)
)

func (ls *legacyStorage) Open(name string, size int64) (storage.File, bool, error) {
	clean := filepath.Clean(name)
	if !filepath.IsLocal(clean) || clean == "." {
		return nil, false, fmt.Errorf("file path is not inside the storage root: %q", name)
	}
	return openFile(filepath.Join(ls.dest, clean), size, ls.perm)
}

func (ls *legacyStorage) RootDir() string { return ls.dest }

func (ls *legacyStorage) Remove(_ *storage.Manifest, keepData bool) error {
	if keepData {
		return nil
	}
	return os.RemoveAll(ls.dest)
}

// openFile opens or creates a torrent file the way rain's file storage does: read-write, synced,
// truncated to the torrent's size. exists reports a file that was already there.
func openFile(path string, size int64, perm os.FileMode) (storage.File, bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), os.ModeDir|perm); err != nil {
		return nil, false, err
	}
	mode := perm &^ 0111
	flags := os.O_RDWR | os.O_SYNC
	f, err := os.OpenFile(path, flags, mode)
	if errors.Is(err, os.ErrNotExist) {
		f, err = os.OpenFile(path, flags|os.O_CREATE, mode)
		if err != nil {
			return nil, false, err
		}
		if err := f.Truncate(size); err != nil {
			_ = f.Close()
			return nil, false, err
		}
		return f, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, false, err
	}
	if fi.Size() != size {
		if err := f.Truncate(size); err != nil {
			_ = f.Close()
			return nil, false, err
		}
	}
	return f, true, nil
}

// finalizeFiles renames every .part file of a placement to its final name. A .part already gone
// with the final name present is a previous run that got this far. Returns the first error.
func finalizeFiles(p Placement) error {
	for _, f := range p.Files {
		part := f.Path + partSuffix
		if !pathExists(part) {
			if pathExists(f.Path) {
				continue
			}
			return fmt.Errorf("neither %s nor its .part file exists", f.Path)
		}
		if err := os.Rename(part, f.Path); err != nil {
			return fmt.Errorf("failed to rename %s: %w", part, err)
		}
	}
	return nil
}
