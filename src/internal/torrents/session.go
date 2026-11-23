package torrents

import (
	"bytes"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"AutoAnimeDownloader/src/internal/logger"

	"github.com/cenkalti/rain/v2/torrent"
)

// Session is the embedded BitTorrent client, backed by github.com/cenkalti/rain/v2.
// It is created once at daemon startup and shared across all call sites (replacing the
// per-operation, stateless qBittorrent HTTP client). It implements TorrentBackend.
type Session struct {
	ses *torrent.Session
	// placements is non-nil in direct library mode (see sessionOptions.placements), and
	// libraryRoot is what its relative paths resolve against.
	placements  *placementStore
	libraryRoot string

	mu         sync.RWMutex
	onComplete func(hash string)
	onFailed   func(hash string, err error)
}

// sessionOptions carries the deviations from the production rain config. It exists for the
// tests, which open real sessions and must not depend on the machine being otherwise idle.
type sessionOptions struct {
	// disableDHT turns rain's DHT node off. Creating a session is the only thing that binds a
	// FIXED port — DefaultConfig.DHTPort (7246/udp) — so with DHT on, every test that opens a
	// session fails with "address already in use" whenever the user's daemon (or a second test
	// binary) is already running. Turning it off also keeps the tests off the network, since a
	// DHT node starts talking to the bootstrap routers as soon as it comes up.
	//
	// Production always leaves DHT on: for a Nyaa magnet whose trackers are down, it is the
	// only way left to find peers.
	disableDHT bool
	// noUpload is disable_seeding: no peer is ever unchoked, and the BEP 6 allowed-fast set — the
	// pieces a CHOKED peer may still request — is empty. Both are needed: with unchoking off but
	// the default allowed-fast set of 10, rain still serves those 10 pieces to every peer.
	// rain's upload speed limit can't do this, 0 means unlimited there.
	noUpload bool
	// placements, when set, is direct library mode: rain gets a placementProvider as its
	// CustomStorage, so a placed torrent writes straight to its library path. Nil keeps rain's
	// own file storage — hardlink mode is byte-for-byte what it was before direct mode existed.
	placements *placementStore
}

// NewSession creates the embedded client. savePath is where torrent files live and keep
// seeding (rain's DataDir); databasePath is the resume database (bbolt), kept outside
// savePath on purpose so resume survives a savePath change.
func NewSession(savePath, databasePath string) (*Session, error) {
	return newSession(savePath, databasePath, sessionOptions{})
}

func newSession(savePath, databasePath string, opts sessionOptions) (*Session, error) {
	rs, err := torrent.NewSession(rainConfig(savePath, databasePath, opts))
	if err != nil {
		return nil, fmt.Errorf("failed to create embedded torrent session: %w", err)
	}

	return &Session{ses: rs, placements: opts.placements, libraryRoot: libraryRootOf(savePath)}, nil
}

// rainConfig is the production rain config plus opts. Apart so the tests can read what a session
// is built with: rain doesn't hand its config back.
func rainConfig(savePath, databasePath string, opts sessionOptions) torrent.Config {
	cfg := torrent.DefaultConfig
	cfg.DataDir = savePath
	cfg.Database = databasePath
	cfg.DataDirIncludesTorrentID = true
	// The app does not expose rain's RPC server; disable it to avoid binding a port.
	cfg.RPCEnabled = false
	if opts.disableDHT {
		cfg.DHTEnabled = false
	}
	if opts.noUpload {
		cfg.UnchokedPeers = 0
		cfg.OptimisticUnchokedPeers = 0
		cfg.AllowedFastSet = 0
	}
	if opts.placements != nil {
		cfg.CustomStorage = &placementProvider{store: opts.placements, root: libraryRootOf(savePath), fallbackDir: savePath, perm: cfg.FilePermissions}
	}
	return cfg
}

// libraryRootOf is the library a session's placements are relative to. The download path is
// always <completed_anime_path>/.torrents (Config.DownloadPath, decisions/031), so the library is
// its parent — the session never needs to be told twice.
func libraryRootOf(savePath string) string {
	if savePath == "" {
		return ""
	}
	return filepath.Dir(savePath)
}

// stopAndWait stops a torrent and waits until rain reports it stopped (its files closed).
func stopAndWait(t *torrent.Torrent) error {
	if err := t.Stop(); err != nil {
		return err
	}
	deadline := time.Now().Add(finalizeStopTimeout)
	for t.Stats().Status != torrent.Stopped {
		if time.Now().After(deadline) {
			return fmt.Errorf("did not stop within %s", finalizeStopTimeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

// RedownloadPlacement downloads a finished direct-mode torrent's missing files again, into the
// same paths (decisions/139). The torrent is removed from rain keeping its data (placedStorage
// leaves the files and the placement alone), the placement is reopened so Open may create the
// missing files (as .part again in part mode), and the same .torrent is added back. The fresh
// torrent has no stored bitfield, so it verifies what is on disk, reuses the files still there
// and fetches only what is gone; organize runs again on its completion.
//
// Not a restart of the same torrent, nor rain's Verify: both leave rain trusting the old
// complete state, and an empty recreated file can pass as finished (decisions/140).
func (s *Session) RedownloadPlacement(hash, incompleteRoot string) error {
	if s.placements == nil {
		return fmt.Errorf("torrent %s is not a direct-mode torrent", hash)
	}
	t := s.ses.GetTorrent(hash)
	if t == nil {
		return fmt.Errorf("torrent %s not found", hash)
	}
	if _, ok := s.placements.get(hash); !ok {
		return fmt.Errorf("torrent %s has no placement", hash)
	}
	// After the removal: no instance of this torrent is left to open files under the reopened state.
	if err := s.readd(t, func() error { return s.placements.reopen(hash, incompleteRoot) }); err != nil {
		return err
	}
	logger.Logger.Info().Str("hash", hash).Msg("Redownloading the missing files of a direct-mode torrent")
	return nil
}

// MovePlacementStaging re-points an unfinished staging torrent at root, the incomplete folder the
// user moved it to (decisions/141), and adds the torrent back so rain checks the moved files: a
// folder copied while the torrent was still writing is older than rain's saved progress, and a
// plain start would trust that progress and later move stale bytes into the library as finished.
// A hash not in the session only gets the new root (its next add starts fresh anyway).
func (s *Session) MovePlacementStaging(hash, root string) error {
	if s.placements == nil {
		return fmt.Errorf("torrent %s is not a direct-mode torrent", hash)
	}
	t := s.ses.GetTorrent(hash)
	if t == nil {
		return s.placements.setIncompleteRoot(hash, root)
	}
	return s.readd(t, func() error { return s.placements.setIncompleteRoot(hash, root) })
}

// readd removes t from rain keeping its data, runs between, and adds the same .torrent back. The
// fresh torrent has no stored bitfield, so it verifies every file it finds (decisions/140).
func (s *Session) readd(t *torrent.Torrent, between func() error) error {
	hash := t.InfoHash().String()
	metainfo, err := t.Torrent()
	if err != nil {
		return fmt.Errorf("torrent %s has no metadata to add back: %w", hash, err)
	}
	if err := s.ses.RemoveTorrent(hash, true); err != nil {
		return fmt.Errorf("failed to remove torrent %s to add it back: %w", hash, err)
	}
	if err := between(); err != nil {
		return err
	}
	t, err = s.ses.AddTorrent(bytes.NewReader(metainfo), &torrent.AddTorrentOptions{ID: hash})
	if err != nil {
		// A pass may have re-added the hash from its magnet in the gap; that torrent is fresh too.
		if t = s.ses.GetTorrent(hash); t == nil {
			return fmt.Errorf("failed to add torrent %s back: %w", hash, err)
		}
	}
	s.armListener(t)
	return nil
}

// finalizeStopTimeout bounds how long FinalizePlacement waits for rain to close a torrent's
// files. rain's Stop is asynchronous: "stopping" lasts until the stop announce reaches the
// trackers, up to ~5s.
const finalizeStopTimeout = 20 * time.Second

// FinalizePlacement gives a completed direct-mode torrent's .part files their final names. The
// torrent is stopped first so no file is open during the rename (Windows refuses to rename an
// open file), then started again to keep seeding from the final names. A placement without part
// files is only marked completed: from then on a missing file is one deleted outside the app
// (decisions/139). Idempotent: an already completed placement returns at once.
func (s *Session) FinalizePlacement(hash string) error {
	return s.finalizePlacement(hash, false)
}

// finalizePlacement is FinalizePlacement; copied says a staging torrent's files were already
// copied into the library (SessionManager does that outside its lock, decisions/141).
func (s *Session) finalizePlacement(hash string, copied bool) error {
	if s.placements == nil {
		return nil
	}
	stored, ok := s.placements.get(hash)
	if !ok || stored.Completed {
		return nil
	}
	if stored.IncompleteRoot != "" {
		return s.finalizeStaged(stored, copied)
	}
	if !stored.PartFiles {
		// Nothing to rename; the mark is what lets Open tell a deleted file from one not written yet.
		stored.Completed = true
		return s.placements.update(stored)
	}
	p := stored.Resolve(s.libraryRoot)
	if len(p.Files) == 0 {
		return fmt.Errorf("torrent %s has no file map yet", hash)
	}
	t := s.ses.GetTorrent(hash)
	if t == nil {
		return fmt.Errorf("torrent %s not found", hash)
	}
	// Complete by the bitfield AND settled: right after a start rain still reports the old
	// bitfield while it allocates and verifies, and renaming then would give a still-empty .part
	// its final name. Organize retries (decisions/140).
	st := t.Stats()
	if !completedFromStats(st) || (st.Status != torrent.Seeding && st.Status != torrent.Stopped) {
		return fmt.Errorf("torrent %s is not complete yet", hash)
	}

	wasRunning := t.Stats().Status != torrent.Stopped
	if wasRunning {
		if err := stopAndWait(t); err != nil {
			return fmt.Errorf("failed to stop torrent %s for the rename: %w", hash, err)
		}
	}

	renameErr := finalizeFiles(p)
	if renameErr == nil {
		stored.Completed = true
		renameErr = s.placements.update(stored)
	}
	if wasRunning {
		if err := t.Start(); err != nil {
			logger.Logger.Warn().Err(err).Str("hash", hash).Msg("Failed to restart the torrent after naming its files")
		}
	}
	if renameErr != nil {
		return renameErr
	}
	logger.Logger.Info().Str("hash", hash).Int("files", len(p.Files)).Msg("Gave the finished torrent's files their final names")
	return nil
}

func (s *Session) Add(magnet string) (string, error) {
	hash, err := parseInfoHash(magnet)
	if err != nil {
		return "", fmt.Errorf("invalid magnet: %w", err)
	}

	// The torrent may already be in the session (e.g. loaded from resume, or re-added by
	// the loop). Check up front instead of matching rain's "duplicate torrent id" error
	// string, which would break silently on any library upgrade that rewords it.
	if s.ses.GetTorrent(hash) != nil {
		logger.Logger.Debug().Str("hash", hash).Msg("Torrent already present, reusing")
		s.armListenerFor(hash)
		return hash, nil
	}

	t, err := s.ses.AddURI(magnet, &torrent.AddTorrentOptions{ID: hash})
	if err != nil {
		return "", fmt.Errorf("failed to add torrent: %w", err)
	}

	s.armListener(t)
	logger.Logger.Info().Str("hash", hash).Str("name", t.Name()).Msg("Added torrent to embedded client")
	return hash, nil
}

func (s *Session) List() []TorrentInfo {
	ts := s.ses.ListTorrents()
	out := make([]TorrentInfo, 0, len(ts))
	for _, t := range ts {
		out = append(out, toInfo(t))
	}
	return out
}

func (s *Session) Get(hash string) (TorrentInfo, bool) {
	t := s.ses.GetTorrent(hash)
	if t == nil {
		return TorrentInfo{}, false
	}
	return toInfo(t), true
}

// Files prefers FileStats() (which brings per-file progress) and falls back to Files() when
// the torrent is stopped — rain frees t.pieces on Stop(), which makes FileStats() fail, but
// t.info survives and Files() still answers. Without metadata neither responds, and then the
// empty list is the right answer: a freshly added magnet does not yet know what it carries.
func (s *Session) Files(hash string) ([]FileInfo, error) {
	t := s.ses.GetTorrent(hash)
	if t == nil {
		return nil, fmt.Errorf("torrent %s not found", hash)
	}

	if stats, err := t.FileStats(); err == nil {
		out := make([]FileInfo, 0, len(stats))
		for _, fs := range stats {
			done := fs.BytesCompleted
			out = append(out, FileInfo{Path: fs.Path(), Size: fs.Length(), BytesCompleted: &done})
		}
		return out, nil
	}

	files, err := t.Files()
	if err != nil {
		return nil, nil
	}
	out := make([]FileInfo, 0, len(files))
	for _, f := range files {
		out = append(out, FileInfo{Path: f.Path(), Size: f.Length()})
	}
	return out, nil
}

func (s *Session) Remove(hash string, keepData bool) error {
	if err := s.ses.RemoveTorrent(hash, keepData); err != nil {
		return fmt.Errorf("failed to remove torrent %s: %w", hash, err)
	}
	return nil
}

func (s *Session) Pause(hash string) error {
	t := s.ses.GetTorrent(hash)
	if t == nil {
		return fmt.Errorf("torrent %s not found", hash)
	}
	if err := t.Stop(); err != nil {
		return fmt.Errorf("failed to pause torrent %s: %w", hash, err)
	}
	logger.Logger.Info().Str("hash", hash).Msg("Paused torrent")
	return nil
}

func (s *Session) Resume(hash string) error {
	t := s.ses.GetTorrent(hash)
	if t == nil {
		return fmt.Errorf("torrent %s not found", hash)
	}
	if err := t.Start(); err != nil {
		return fmt.Errorf("failed to resume torrent %s: %w", hash, err)
	}
	// Pausing made NotifyStop fire (with a nil error), which ended the one-shot listener
	// goroutine armed by Add/SetCallbacks. Without re-arming, this torrent would finish
	// downloading without ever enqueuing JobOrganize — the episode would never be
	// hardlinked into the library. Re-arming may leave two listeners on a
	// pause/resume/pause/resume torrent; that is safe, because JobOrganize is idempotent.
	s.armListener(t)
	logger.Logger.Info().Str("hash", hash).Msg("Resumed torrent")
	return nil
}

func (s *Session) Announce(hash string) error {
	t := s.ses.GetTorrent(hash)
	if t == nil {
		return fmt.Errorf("torrent %s not found", hash)
	}
	// rain's Announce returns nothing — it queues the announce and returns immediately.
	t.Announce()
	logger.Logger.Info().Str("hash", hash).Msg("Forced torrent re-announce")
	return nil
}

func (s *Session) SetCallbacks(onComplete func(hash string), onFailed func(hash string, err error)) {
	s.mu.Lock()
	s.onComplete = onComplete
	s.onFailed = onFailed
	s.mu.Unlock()

	// Arm listeners for torrents already loaded from resume that are still in progress.
	// Already-completed torrents are handled by startup reconciliation, not by events
	// (their one-shot NotifyComplete already fired before this run).
	for _, t := range s.ses.ListTorrents() {
		if t.Stats().Status != torrent.Seeding {
			s.armListener(t)
		}
	}
}

func (s *Session) Close() error {
	return s.ses.Close()
}

// armListenerFor arms a listener by hash (used when a torrent is already present).
func (s *Session) armListenerFor(hash string) {
	if t := s.ses.GetTorrent(hash); t != nil && t.Stats().Status != torrent.Seeding {
		s.armListener(t)
	}
}

// armListener spawns a one-shot goroutine translating rain's per-torrent events into the
// registered callbacks. NotifyClose is included so the goroutine never leaks when the
// torrent is removed or the session closes.
func (s *Session) armListener(t *torrent.Torrent) {
	hash := t.InfoHash().String()
	go func() {
		select {
		case <-t.NotifyComplete():
			s.mu.RLock()
			cb := s.onComplete
			s.mu.RUnlock()
			if cb != nil {
				cb(hash)
			}
		case err := <-t.NotifyStop():
			if err != nil {
				s.mu.RLock()
				cb := s.onFailed
				s.mu.RUnlock()
				if cb != nil {
					cb(hash, err)
				}
			}
		case <-t.NotifyClose():
		}
	}()
}

// completedFromStats reports whether every piece is on disk. It is deliberately not
// derived from Status: pausing takes a finished torrent out of Seeding, and organize/
// reconcile gate on this field.
func completedFromStats(st torrent.Stats) bool {
	return st.Pieces.Total > 0 && st.Pieces.Have >= st.Pieces.Total
}

func toInfo(t *torrent.Torrent) TorrentInfo {
	// One Stats() only: it is a blocking round-trip into the torrent's goroutine, not a getter.
	st := t.Stats()

	var eta *int64
	if st.ETA != nil {
		secs := int64(st.ETA.Seconds())
		eta = &secs
	}

	return TorrentInfo{
		Hash:             t.InfoHash().String(),
		Name:             t.Name(),
		DataDir:          t.Dir(),
		Completed:        completedFromStats(st),
		Status:           statusSlug(st.Status),
		BytesCompleted:   st.Bytes.Completed,
		BytesTotal:       st.Bytes.Total,
		BytesUploaded:    st.Bytes.Uploaded,
		DownloadSpeed:    st.Speed.Download,
		UploadSpeed:      st.Speed.Upload,
		PeersTotal:       st.Peers.Total,
		PiecesHave:       st.Pieces.Have,
		PiecesTotal:      st.Pieces.Total,
		ETASeconds:       eta,
		SeededForSeconds: int64(st.SeededFor.Seconds()),
		AddedAt:          t.AddedAt(),
		StopError:        st.Error,
	}
}

// parseInfoHash extracts the BitTorrent v1 info hash from a magnet link and returns it as
// lowercase hex (40 chars), matching torrent.InfoHash.String(). The result is used as the
// torrent's ID in rain, so it is kept compatible with rain's own magnet parsing
// (internal/magnet): same hex/base32 accepted forms, same case-sensitive base32 decode.
//
// It is deliberately not a full clone of rain's parser: rain only looks at the first xt
// param, while this scans all of them and prefers `urn:btih:`. That makes hybrid v1/v2
// magnets that list `urn:btmh:` first parse here instead of being rejected outright.
// Only `urn:btih:` (v1) is supported — a magnet with no btih xt is an error.
// InfoHash is parseInfoHash for the daemon: the disk guard keys its reservations by the hash a
// magnet will get BEFORE adding it, so the N episodes of one pack reserve its size once.
func InfoHash(magnet string) (string, error) { return parseInfoHash(magnet) }

func parseInfoHash(magnet string) (string, error) {
	u, err := url.Parse(magnet)
	if err != nil {
		return "", err
	}
	if u.Scheme != "magnet" {
		return "", fmt.Errorf("not a magnet link")
	}
	xts := u.Query()["xt"]
	if len(xts) == 0 {
		return "", fmt.Errorf("missing xt param")
	}

	const btihPrefix = "urn:btih:"
	raw := ""
	for _, xt := range xts {
		if strings.HasPrefix(xt, btihPrefix) {
			raw = xt[len(btihPrefix):]
			break
		}
	}
	if raw == "" {
		return "", fmt.Errorf("unsupported xt param(s): %s", strings.Join(xts, ", "))
	}

	var b []byte
	switch len(raw) {
	case 40:
		b, err = hex.DecodeString(raw)
	case 32:
		// Uppercase-only, like rain's base32.StdEncoding.DecodeString — normalizing the
		// case here would accept magnets that rain itself rejects at AddURI time.
		b, err = base32.StdEncoding.DecodeString(raw)
	default:
		return "", fmt.Errorf("info hash must be 32 or 40 characters, got %d", len(raw))
	}
	if err != nil {
		return "", err
	}
	if len(b) != 20 {
		return "", fmt.Errorf("invalid info hash length %d", len(b))
	}
	return hex.EncodeToString(b), nil
}
