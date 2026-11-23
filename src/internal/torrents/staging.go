package torrents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"AutoAnimeDownloader/src/internal/logger"

	"github.com/cenkalti/rain/v2/torrent"
)

// Staging (decisions/141): a direct-mode torrent with an IncompleteRoot downloads into
// <IncompleteRoot>/<hash>/ on a fast drive and only reaches the library once it is finished, as
// one sequential write per file. Finishing has three phases:
//
//  1. copy each staged file to "<final>.aad-moving" in the library while the torrent keeps
//     seeding from staging (a finished torrent only reads) — slow, so SessionManager runs it
//     without its lock;
//  2. switch, short: stop the torrent, rename every ".aad-moving" to its final name, mark the
//     placement Completed, start it again — it seeds from the library from then on;
//  3. remove the torrent's staging folder.
//
// A final name in the library only ever appears by renaming a whole, synced copy, which is what
// lets placedStorage.Open trust a file of the right size at the final path after a crash.

// linkFile is os.Link, swappable so the tests can force the copy (their temp folders share one
// filesystem, where the link always succeeds).
var linkFile = os.Link

// copyBufferSize is the read/write unit of a staging copy: large, so the library disk sees long
// sequential writes.
const copyBufferSize = 8 << 20

// StatusMoving is the torrents-list slug for a finished torrent whose files are being copied
// from staging into the library. Like StatusQueued it never comes from rain: SessionManager
// writes it (moveTracker.overlay).
const StatusMoving = "moving"

// settledFileSizes returns the size of each of a torrent's files by its rain path, once the
// torrent is complete AND settled. Right after a start rain still reports the old bitfield while
// it allocates and verifies, and copying or renaming then would act on files that are not whole
// (decisions/140).
func settledFileSizes(t *torrent.Torrent, hash string) (map[string]int64, error) {
	st := t.Stats()
	if !completedFromStats(st) || (st.Status != torrent.Seeding && st.Status != torrent.Stopped) {
		return nil, fmt.Errorf("torrent %s is not complete yet", hash)
	}
	list, err := t.Files()
	if err != nil {
		return nil, fmt.Errorf("torrent %s: %w", hash, err)
	}
	sizes := make(map[string]int64, len(list))
	for _, f := range list {
		sizes[f.Path()] = f.Length()
	}
	return sizes, nil
}

// stagedToCopy returns the files of a resolved staging placement that are not whole at their final
// library path yet, and their total size.
func stagedToCopy(p Placement, sizes map[string]int64) ([]PlacedFile, int64) {
	var out []PlacedFile
	var total int64
	for _, f := range p.Files {
		if hasSize(f.Path, sizes[f.Torrent]) {
			continue
		}
		out = append(out, f)
		total += sizes[f.Torrent]
	}
	return out, total
}

// copyStagedFiles is phase 1: every file not whole at its final path is copied from staging to
// "<final>.aad-moving" and synced. On the same filesystem a hard link is the whole copy, instant
// and free; anywhere else (another drive, a filesystem without links) the bytes are copied.
// progress, when set, gets the bytes done so far. A failed or cancelled copy removes every
// ".aad-moving" file it created, the whole ones of a pack included: a delete with keep data during
// the copy must not leave half a pack of them in the library.
func copyStagedFiles(ctx context.Context, p Placement, sizes map[string]int64, progress func(int64)) (err error) {
	todo, _ := stagedToCopy(p, sizes)
	var created []string
	defer func() {
		if err != nil {
			for _, path := range created {
				_ = os.Remove(path)
			}
		}
	}()
	var done int64
	report := func(n int64) {
		if progress != nil {
			progress(n)
		}
	}
	for _, f := range todo {
		if err := ctx.Err(); err != nil {
			return err
		}
		src := p.StagingPath(f)
		dst := f.Path + movingSuffix
		if !hasSize(src, sizes[f.Torrent]) {
			return fmt.Errorf("the staged file %s is missing or incomplete", src)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", filepath.Dir(dst), err)
		}
		_ = os.Remove(dst)
		created = append(created, dst)
		if linkFile(src, dst) != nil {
			if err := copyFile(ctx, src, dst, func(n int64) { report(done + n) }); err != nil {
				_ = os.Remove(dst)
				return err
			}
		}
		done += sizes[f.Torrent]
		report(done)
	}
	return nil
}

// copyFile copies src to dst sequentially, in copyBufferSize chunks, checking ctx between chunks,
// and syncs dst before returning.
func copyFile(ctx context.Context, src, dst string, progress func(int64)) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	buf := make([]byte, copyBufferSize)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			_ = out.Close()
			return err
		}
		n, readErr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				_ = out.Close()
				return fmt.Errorf("failed to write %s: %w", dst, err)
			}
			written += int64(n)
			progress(written)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = out.Close()
			return fmt.Errorf("failed to read %s: %w", src, readErr)
		}
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("failed to sync %s: %w", dst, err)
	}
	return out.Close()
}

// finalizeStaged is phases 2 and 3 for a staging placement, and phase 1 too unless copied says
// SessionManager already ran it outside its lock.
func (s *Session) finalizeStaged(stored Placement, copied bool) error {
	hash := stored.Hash
	p := stored.Resolve(s.libraryRoot)
	if len(p.Files) == 0 {
		return fmt.Errorf("torrent %s has no file map yet", hash)
	}
	t := s.ses.GetTorrent(hash)
	if t == nil {
		return fmt.Errorf("torrent %s not found", hash)
	}
	sizes, err := settledFileSizes(t, hash)
	if err != nil {
		return err
	}
	if !copied {
		if err := copyStagedFiles(context.Background(), p, sizes, nil); err != nil {
			return fmt.Errorf("failed to copy torrent %s into the library: %w", hash, err)
		}
	}
	todo, _ := stagedToCopy(p, sizes)
	for _, f := range todo {
		if !hasSize(f.Path+movingSuffix, sizes[f.Torrent]) {
			return fmt.Errorf("torrent %s: %s is not copied into the library yet", hash, f.Path)
		}
	}

	wasRunning := t.Stats().Status != torrent.Stopped
	if wasRunning {
		if err := stopAndWait(t); err != nil {
			return fmt.Errorf("failed to stop torrent %s for the move: %w", hash, err)
		}
	}
	var switchErr error
	for _, f := range todo {
		if err := os.Rename(f.Path+movingSuffix, f.Path); err != nil {
			switchErr = fmt.Errorf("failed to give %s its final name: %w", f.Path, err)
			break
		}
	}
	if switchErr == nil {
		stored.Completed = true
		switchErr = s.placements.update(stored)
	}
	if wasRunning {
		if err := t.Start(); err != nil {
			logger.Logger.Warn().Err(err).Str("hash", hash).Msg("Failed to restart the torrent after moving its files into the library")
		}
	}
	if switchErr != nil {
		return switchErr
	}
	if dir := p.StagingDir(); dir != "" {
		if err := os.RemoveAll(dir); err != nil {
			logger.Logger.Warn().Err(err).Str("hash", hash).Msg("Failed to clean up the incomplete folder after the move; the sweep will retry")
		}
	}
	logger.Logger.Info().Str("hash", hash).Int("files", len(todo)).Msg("Moved the finished torrent's files from the incomplete folder into the library")
	return nil
}

// moveTracker runs the phase-1 copies: one at a time — two interleaved sequential writes are
// random writes again on an SMR disk — each cancellable (a delete of that torrent, the daemon
// shutting down), and with the progress the torrents list shows as "moving".
type moveTracker struct {
	serial sync.Mutex
	mu     sync.Mutex
	base   context.Context
	stop   context.CancelFunc
	byHash map[string]*moveState
}

type moveState struct {
	done   atomic.Int64
	total  int64
	cancel context.CancelFunc
}

func newMoveTracker() *moveTracker {
	ctx, stop := context.WithCancel(context.Background())
	return &moveTracker{base: ctx, stop: stop, byHash: map[string]*moveState{}}
}

// run copies a resolved staging placement's files into the library (phase 1), tracked under hash.
func (mt *moveTracker) run(hash string, p Placement, sizes map[string]int64) error {
	mt.serial.Lock()
	defer mt.serial.Unlock()
	_, total := stagedToCopy(p, sizes)
	if total == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(mt.base)
	defer cancel()
	state := &moveState{total: total, cancel: cancel}
	mt.mu.Lock()
	mt.byHash[hash] = state
	mt.mu.Unlock()
	defer func() {
		mt.mu.Lock()
		delete(mt.byHash, hash)
		mt.mu.Unlock()
	}()
	logger.Logger.Info().Str("hash", hash).Int64("bytes", total).Msg("Copying the finished torrent from the incomplete folder into the library")
	return copyStagedFiles(ctx, p, sizes, func(n int64) { state.done.Store(n) })
}

// cancel stops hash's copy, if one runs.
func (mt *moveTracker) cancel(hash string) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	if st, ok := mt.byHash[hash]; ok {
		st.cancel()
	}
}

// cancelAll stops every copy, running or to come: the daemon is shutting down.
func (mt *moveTracker) cancelAll() {
	mt.stop()
}

// overlay writes the "moving" slug and the copy's progress onto the torrents being copied.
func (mt *moveTracker) overlay(infos []TorrentInfo) []TorrentInfo {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	if len(mt.byHash) == 0 {
		return infos
	}
	for i := range infos {
		if st, ok := mt.byHash[infos[i].Hash]; ok {
			infos[i].Status = StatusMoving
			infos[i].MoveProgress = float64(st.done.Load()) / float64(st.total)
		}
	}
	return infos
}
