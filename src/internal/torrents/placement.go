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

// ErrPlacedFileMissing is what Open returns for a finished torrent whose library file is gone:
// deleted outside the app. Recreating it would make rain download it again; the user asked for a
// warning and nothing else (decisions/139), so the torrent stops with this error instead.
var ErrPlacedFileMissing = errors.New("library file of a finished torrent is missing")

// placementsFileName is where the placements live, next to session.db, for the same reason as
// queue.json: it is torrent-client state and must follow the resume database.
const placementsFileName = "placements.json"

// partSuffix marks a file that is still downloading when PlacementRequest.PartFiles is set. A
// media server skips it: ".part" is no video extension.
const partSuffix = ".part"

// movingSuffix marks a library file being copied in from the staging folder (decisions/141). It
// only gets its final name by a rename once the copy is whole and synced, so a final name in the
// library always holds a complete file.
const movingSuffix = ".aad-moving"

// ErrIncompleteFolderUnavailable is what Open returns when a staging torrent's incomplete folder
// is gone (its drive unmounted). The folder is never created here: on an unmounted drive that would
// write the download onto the system disk under the mount point (decisions/141). The torrent stops
// with this error, and the download queue starts it again on its next pass.
var ErrIncompleteFolderUnavailable = errors.New("the incomplete downloads folder is unavailable")

// ErrDownloadRootUnavailable is what Open returns when a placement's download root (not the
// library) is gone, an unmounted drive: the root is never created, or the torrent would fill the
// disk its mount point sits on. The torrent waits for it, as for the incomplete folder
// (decisions/154).
var ErrDownloadRootUnavailable = errors.New("the download folder is unavailable")

// PlacementRequest is what the daemon decides about a torrent before adding it.
type PlacementRequest struct {
	// Hash is the torrent's info hash (rain's torrent id).
	Hash string `json:"hash"`
	// SeriesDir is the <Series> folder every file of the torrent goes under, relative to its root
	// ("Frieren"). Placement.Resolve makes it absolute.
	SeriesDir string `json:"series_dir"`
	// RootPath is the absolute download root the torrent lives in, as the app sees it, and RootName
	// its name (decisions/154). Empty = the library root a backend resolves against, which is what
	// every placement from before several roots means.
	RootPath string `json:"root_path,omitempty"`
	RootName string `json:"root_name,omitempty"`
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
	// PartFiles writes "<name>.part" until FinalizePlacement renames the files. Ignored while the
	// placement stages (IncompleteRoot): staged files keep their final names.
	PartFiles bool `json:"part_files,omitempty"`
	// IncompleteRoot, when set, is the absolute staging folder the torrent downloads into: its files
	// live at <IncompleteRoot>/<hash>/<library-relative path> until FinalizePlacement moves them into
	// the library (decisions/141). Frozen with the rest of the placement; "" writes straight to the
	// library.
	IncompleteRoot string `json:"incomplete_root,omitempty"`
}

// PlacedFile is one torrent file and where it lives.
type PlacedFile struct {
	// Torrent is the path rain uses: "<torrent name>/<path inside>" for a multi-file torrent.
	Torrent string `json:"torrent"`
	// Path is the final path, relative to the library root ("Frieren/Season 01/ep05.mkv") as
	// stored, absolute once the placement went through Resolve.
	Path string `json:"path"`
	// Skipped marks a file the daemon doesn't want: an episode of a pack the user already has in
	// Shoko, for an anime set to remove them (SkipPlacedFiles). qBittorrent never downloads it
	// (priority 0); one already written is deleted by the daemon. A skipped file missing from disk
	// is no loss, and it is no library path (decisions/153).
	Skipped bool `json:"skipped,omitempty"`
}

// Placement is a PlacementRequest plus the frozen file map.
type Placement struct {
	PlacementRequest
	// Files is empty until rain prepares the torrent (its metadata arrived).
	Files []PlacedFile `json:"files,omitempty"`
	// Completed is set by FinalizePlacement when the torrent finished: after every .part file got
	// its final name, or right away without PartFiles. From then on a missing file is one deleted
	// outside the app, and Open refuses to recreate it (ErrPlacedFileMissing).
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
	// root is the library root Resolve joined the paths to; StagingPath needs the relative path
	// back. Never stored.
	root string
}

// Resolve returns the placement with SeriesDir and every file path joined to root. Everything
// that touches the disk works on a resolved placement; the store keeps the relative one. An empty
// root leaves the paths relative.
func (p Placement) Resolve(root string) Placement {
	// A placement in another download root resolves against it, whatever the caller's library is.
	if p.RootPath != "" {
		root = p.RootPath
	}
	if root == "" {
		return p
	}
	out := p
	out.root = root
	out.SeriesDir = filepath.Join(root, p.SeriesDir)
	out.Files = make([]PlacedFile, len(p.Files))
	for i, f := range p.Files {
		f.Path = filepath.Join(root, f.Path)
		out.Files[i] = f
	}
	return out
}

// DiskPath is where a placed file is on disk right now: its final path once the placement is
// finalized; before that, its staging path when the placement stages, else its .part name when it
// writes part files. (placedStorage.Open also takes a whole file already at the final path.)
func (p Placement) DiskPath(f PlacedFile) string {
	switch {
	case p.Completed:
		return f.Path
	case p.IncompleteRoot != "":
		return p.StagingPath(f)
	case p.PartFiles:
		return f.Path + partSuffix
	}
	return f.Path
}

// StagingDir is the torrent's own folder inside its staging root, "" when it doesn't stage.
// Everything it stages is below it, so cleaning up is removing this one folder.
func (p Placement) StagingDir() string {
	if p.IncompleteRoot == "" || p.Hash == "" {
		return ""
	}
	return filepath.Join(p.IncompleteRoot, p.Hash)
}

// StagingPath is where f downloads while the placement stages: its library-relative path below
// StagingDir, under its final name — never .part (decisions/141).
func (p Placement) StagingPath(f PlacedFile) string {
	rel := f.Path
	if p.root != "" {
		if r, err := filepath.Rel(p.root, f.Path); err == nil {
			rel = r
		}
	}
	return filepath.Join(p.StagingDir(), rel)
}

// VideoPaths returns the final paths of the placement's video files, sorted. These are what a
// direct-mode torrent records as its library paths.
func (p Placement) VideoPaths() []string {
	var out []string
	for _, f := range p.Files {
		if !f.Skipped && nyaa.IsVideoFile(f.Path) {
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

// setSkipped marks the files whose torrent paths are given Skipped (see PlacedFile.Skipped) and
// returns the placement as saved. A hash with no placement, or one whose files aren't planned yet
// (no metadata), is an error: there is nothing to mark.
func (s *placementStore) setSkipped(hash string, torrentPaths []string) (Placement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[hash]
	if !ok || len(p.Files) == 0 {
		return Placement{}, fmt.Errorf("torrent %s has no planned files to skip", hash)
	}
	want := make(map[string]bool, len(torrentPaths))
	for _, t := range torrentPaths {
		want[t] = true
	}
	files := append([]PlacedFile(nil), p.Files...)
	changed := false
	for i := range files {
		if want[files[i].Torrent] && !files[i].Skipped {
			files[i].Skipped = true
			changed = true
		}
	}
	if !changed {
		return p, nil
	}
	old := p
	p.Files = files
	s.byID[hash] = p
	if err := s.saveLocked(); err != nil {
		s.byID[hash] = old
		return Placement{}, err
	}
	return p, nil
}

// reopen clears Completed: the placement's missing files may be written again (the bulk
// "redownload missing files"). It also takes incompleteRoot, the incomplete folder configured now:
// a redownload is a new download, and the files that are gone stage like any other
// (decisions/141); the ones still in the library are reused in place. A hash with no placement is
// an error.
func (s *placementStore) reopen(hash, incompleteRoot string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[hash]
	if !ok {
		return fmt.Errorf("torrent %s has no placement", hash)
	}
	if !p.Completed {
		return nil
	}
	prev := p
	p.Completed = false
	p.IncompleteRoot = incompleteRoot
	s.byID[hash] = p
	if err := s.saveLocked(); err != nil {
		s.byID[hash] = prev
		return err
	}
	return nil
}

// setIncompleteRoot re-points an unfinished staging placement at root: the user moved the
// incomplete folder and changed incomplete_path (decisions/141). A finished one, or a hash
// without a placement, is an error.
func (s *placementStore) setIncompleteRoot(hash, root string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[hash]
	if !ok || p.Completed || p.IncompleteRoot == "" {
		return fmt.Errorf("torrent %s has no unfinished staging placement", hash)
	}
	prev := p
	p.IncompleteRoot = root
	s.byID[hash] = p
	if err := s.saveLocked(); err != nil {
		s.byID[hash] = prev
		return err
	}
	return nil
}

// setRoot re-points a placement at its download root's new path ("" = the library): the user
// moved that folder and edited download_roots (decisions/154).
func (s *placementStore) setRoot(hash, root string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[hash]
	if !ok {
		return fmt.Errorf("torrent %s has no placement", hash)
	}
	prev := p
	p.RootPath = root
	s.byID[hash] = p
	if err := s.saveLocked(); err != nil {
		s.byID[hash] = prev
		return err
	}
	return nil
}

// missingFolder is the folder this placement writes into that isn't there, "" when none: its
// download root (decisions/154), or, while it stages, its incomplete folder (decisions/141). A
// backend never starts a torrent into one, as it would be created on the disk under its mount point.
func (p Placement) missingFolder() string {
	if p.RootPath != "" && !isDir(p.RootPath) {
		return p.RootPath
	}
	if p.IncompleteRoot != "" && !p.Completed && !isDir(p.IncompleteRoot) {
		return p.IncompleteRoot
	}
	return ""
}

// RootOr is the download root the placement lives in: its own, else library, the root every
// placement from before several roots lives in (decisions/154).
func (p Placement) RootOr(library string) string {
	if p.RootPath != "" {
		return p.RootPath
	}
	return library
}

// ownerOf returns the hash of the placement in root that claims path (final or .part name,
// relative to that root), or "". library is what a placement without its own root lives in: the
// same relative path in two roots is two different files.
func (s *placementStore) ownerOf(root, library, path string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for hash, p := range s.byID {
		if p.RootOr(library) != root {
			continue
		}
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

// hasSize reports whether path is a regular file of exactly size bytes.
func hasSize(path string, size int64) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Size() == size
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
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
	root := p.RootOr(ps.root)
	exists := func(rel string) bool { return pathExists(filepath.Join(root, rel)) }
	files, err := planFiles(p.PlacementRequest, paths, exists, func(rel string) bool {
		owner := ps.store.ownerOf(root, ps.root, rel)
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
	if stored.RootPath != "" && !isDir(stored.RootPath) {
		return nil, false, fmt.Errorf("%w: %s", ErrDownloadRootUnavailable, stored.RootPath)
	}
	for _, f := range p.Files {
		if f.Torrent != name {
			continue
		}
		if p.Completed {
			// A skipped file is one the daemon deleted itself (decisions/153), not one the user
			// deleted (decisions/139): a re-added torrent writes it again, and the daemon deletes it
			// again once the torrent finished. Refusing it would stop the torrent on a file
			// PlacedFilesMissing doesn't count, and the failure handler would drop it.
			if !pathExists(f.Path) && !f.Skipped {
				return nil, false, fmt.Errorf("%w: %s", ErrPlacedFileMissing, f.Path)
			}
			return openFile(f.Path, size, ps.perm)
		}
		// Not finished, but a whole file is already at the final path: a crash between the
		// finalize's rename (of a .part, or of a copy from staging) and its save, or a redownload
		// reusing what is still in the library (decisions/140). It is this torrent's — planning
		// never maps onto a file it doesn't own. Opening the .part or staging name instead would
		// create an empty file and download the finished data again.
		if hasSize(f.Path, size) {
			return openFile(f.Path, size, ps.perm)
		}
		// The staging root itself is never created, only the folders below it (decisions/141).
		if p.IncompleteRoot != "" && !isDir(p.IncompleteRoot) {
			return nil, false, fmt.Errorf("%w: %s", ErrIncompleteFolderUnavailable, p.IncompleteRoot)
		}
		return openFile(p.DiskPath(f), size, ps.perm)
	}
	return nil, false, fmt.Errorf("torrent %s has no placed file for %q", ps.hash, name)
}

// RootDir is the series folder: TorrentInfo.DataDir means "where this torrent's files are".
func (ps *placedStorage) RootDir() string {
	p, _ := ps.store.get(ps.hash)
	return filepath.Join(p.RootOr(ps.root), p.SeriesDir)
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
	// Staged files first: the torrent's own folder in the staging root (decisions/141). The root is
	// never touched, and an unreachable one simply has nothing to remove.
	if dir := p.StagingDir(); dir != "" {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("failed to delete the staged files in %s: %w", dir, err)
		}
	}
	dirs := map[string]bool{}
	for _, f := range p.Files {
		paths := []string{f.Path, f.Path + partSuffix, f.Path + movingSuffix}
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
			// A skipped file may never have been written, or already be deleted (decisions/153).
			if f.Skipped || pathExists(f.Path) {
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
