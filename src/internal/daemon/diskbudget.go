package daemon

import (
	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/nyaa"
	"AutoAnimeDownloader/src/internal/torrents"
	"fmt"
	"sync"
)

// diskBudget is the disk guard's view of one pass: the bytes the torrents already in the session
// still have to write, plus the size of every torrent the pass adds. Free space alone answers "is
// there room NOW", and a torrent only takes its room as it downloads — so each loose episode of a
// pass fit on its own and 130 GiB went into 6 GiB of headroom (decisions/101).
//
// Shared by the animes of a pass, which run in parallel: reserve checks and counts under one lock,
// or two episodes would both see the last free bytes. Keyed by infohash, because the N episodes of
// a pack add the same magnet N times and must pay for it once.
//
// nil = no budget, the bare checkRootBudgetSpace: the tests and RunAnimeDebug, which add nothing.
type diskBudget struct {
	mu       sync.Mutex
	reserved int64
	// staged and stagedPending exist for the incomplete folder (decisions/141), and only count
	// when it is on another filesystem than the library. staged is what that folder's volume
	// still has to take: what staging torrents still have to download, plus what this pass adds.
	// stagedPending is the library volume's extra: the bytes staging torrents hold that have not
	// moved into the library yet (every byte of them lands there).
	staged        int64
	stagedPending int64
	counted       map[string]bool

	// Several download roots (decisions/154). reserved and stagedPending stay the totals; byRoot and
	// stagedByRoot hold the share of them on roots other than library, the rest is the library's.
	// pending and pendingStaged are each session torrent's share, by hash, so withRoots can spread
	// them over the roots their placements are in. overrides is AnimeSettings.Root by media id, and
	// countedRoot the root a reserved key went to.
	library       string
	byRoot        map[string]int64
	stagedByRoot  map[string]int64
	pending       map[string]int64
	pendingStaged map[string]int64
	// stagedDLByRoot is the part of staged (what staging torrents still download into the
	// incomplete folder) bound for roots other than the library, and pendingStagedDL each session
	// torrent's share: a root on the incomplete folder's drive also takes the other roots' staging
	// downloads.
	stagedDLByRoot  map[string]int64
	pendingStagedDL map[string]int64
	overrides       map[int]string
	countedRoot     map[string]files.DownloadRoot
}

// newDiskBudget reserves what each incomplete torrent of the session still has to write. A torrent
// whose metadata hasn't arrived (every queued one: it never started) has BytesTotal 0, and its size
// comes from the saved record instead — so after a restart the queue still counts.
//
// ponytail: a torrent with neither (a record from before torrent_size existed, or a hand-pasted
// magnet) reserves nothing until its metadata arrives; it can't be guessed.
func newDiskBudget(session []torrents.TorrentInfo, saved []files.EpisodeStruct) *diskBudget {
	recorded := make(map[string]int64, len(saved))
	for _, ep := range saved {
		if ep.TorrentSize > 0 {
			recorded[ep.EpisodeHash] = ep.TorrentSize
		}
	}

	b := &diskBudget{counted: make(map[string]bool, len(session)), pending: map[string]int64{}, pendingStaged: map[string]int64{}, pendingStagedDL: map[string]int64{}}
	for _, t := range session {
		b.counted[t.Hash] = true
		if t.Completed {
			// Finished but still in the incomplete folder: all of it is yet to land in the library.
			if t.Staged {
				b.stagedPending += t.BytesTotal
				b.pendingStaged[t.Hash] += t.BytesTotal
			}
			continue
		}
		total := t.BytesTotal
		if total == 0 {
			total = recorded[t.Hash]
		}
		// The bitfield survives a pause and BytesCompleted doesn't (see TorrentInfo.PiecesHave):
		// without it a paused half-done torrent would reserve its full size again.
		written := t.BytesCompleted
		if t.PiecesTotal > 0 {
			written = max(written, total*int64(t.PiecesHave)/int64(t.PiecesTotal))
		}
		b.reserved += max(total-written, 0)
		b.pending[t.Hash] += max(total-written, 0)
		if t.Staged {
			b.staged += max(total-written, 0)
			b.pendingStagedDL[t.Hash] += max(total-written, 0)
			b.stagedPending += min(written, total)
			b.pendingStaged[t.Hash] += min(written, total)
		}
	}
	return b
}

// withRoots spreads what the session's torrents still have to write over the download roots their
// placements are in, and keeps each anime's root override (decisions/154). Without it everything
// counts on the library, which is right with one root. Returns b, for chaining.
func (b *diskBudget) withRoots(configs *files.Config, backend torrents.TorrentBackend, settings map[int]files.AnimeSettings) *diskBudget {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.library = configs.CompletedAnimePath
	b.byRoot, b.stagedByRoot, b.stagedDLByRoot = map[string]int64{}, map[string]int64{}, map[string]int64{}
	if backend != nil {
		for _, p := range backend.Placements() {
			root := p.RootOr(b.library)
			if root == b.library {
				continue
			}
			b.byRoot[root] += b.pending[p.Hash]
			b.stagedByRoot[root] += b.pendingStaged[p.Hash]
			b.stagedDLByRoot[root] += b.pendingStagedDL[p.Hash]
		}
	}
	b.overrides = map[int]string{}
	for id, s := range settings {
		if s.Root != nil && *s.Root != "" {
			b.overrides[id] = *s.Root
		}
	}
	return b
}

// override is the root an anime's settings name, "" for none.
func (b *diskBudget) override(mediaID int) string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.overrides[mediaID]
}

// onRootLocked is what is reserved on root's volume, what staging torrents hold for it, and what
// they still download for it: every root on the same filesystem counts, since they share its free
// space.
func (b *diskBudget) onRootLocked(root string) (reserved, stagedPending, stagedDL int64) {
	share := func(total int64, byRoot map[string]int64) (lib int64) {
		lib = total
		for r, n := range byRoot {
			if r != b.library {
				lib -= n
			}
		}
		return lib
	}
	sameVolume := func(r string) bool { return r == root || sameFilesystem(r, root) }
	if b.library == "" || sameVolume(b.library) {
		reserved, stagedPending, stagedDL = share(b.reserved, b.byRoot), share(b.stagedPending, b.stagedByRoot), share(b.staged, b.stagedDLByRoot)
	}
	add := func(byRoot map[string]int64, into *int64) {
		for r, n := range byRoot {
			if r != b.library && sameVolume(r) {
				*into += n
			}
		}
	}
	add(b.byRoot, &reserved)
	add(b.stagedByRoot, &stagedPending)
	add(b.stagedDLByRoot, &stagedDL)
	return reserved, stagedPending, stagedDL
}

// checkRootLocked is the gate for one root with what the budget holds: its own volume's
// reservations, and the other roots' staging downloads when the incomplete folder is on its drive.
func (b *diskBudget) checkRootLocked(configs *files.Config, root string, sizeBytes int64) error {
	reserved, stagedPending, stagedDL := b.onRootLocked(root)
	return checkRootBudgetSpace(configs, root, sizeBytes, reserved, b.staged, stagedPending, b.staged-stagedDL)
}

// check answers whether a torrent of sizeBytes would fit on one of the download roots on top of
// what's reserved there, without reserving it. For the choices made before any add: packsThatFit,
// the report, and DiskLow.
func (b *diskBudget) check(configs *files.Config, sizeBytes int64) error {
	roots := availableRoots(configs)
	if len(roots) == 0 {
		return errNoDownloadRoot
	}
	var err error
	for _, root := range roots {
		if b == nil {
			err = checkRootBudgetSpace(configs, root.Path, sizeBytes, 0, 0, 0, 0)
		} else {
			b.mu.Lock()
			err = b.checkRootLocked(configs, root.Path, sizeBytes)
			b.mu.Unlock()
		}
		if err == nil {
			return nil
		}
	}
	return err
}

// checkOn is check for one root: the torrent's own, for a redownload that writes into its placement.
func (b *diskBudget) checkOn(configs *files.Config, root string, sizeBytes int64) error {
	if b == nil {
		return checkRootBudgetSpace(configs, root, sizeBytes, 0, 0, 0, 0)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.checkRootLocked(configs, root, sizeBytes)
}

// addOn is add for a torrent writing into root (its placement's): counted on that root's share
// too, so the next redownload into it sees these bytes (decisions/154).
func (b *diskBudget) addOn(root string, sizeBytes int64) {
	b.add(sizeBytes)
	b.mu.Lock()
	defer b.mu.Unlock()
	if root != b.library && b.library != "" {
		if b.byRoot == nil {
			b.byRoot = map[string]int64{}
		}
		b.byRoot[root] += sizeBytes
	}
}

// add counts sizeBytes as written by a torrent started outside reserve (the bulk "redownload
// missing files"): on the library, and on the incomplete folder when it stages.
func (b *diskBudget) add(sizeBytes int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reserved += sizeBytes
	b.staged += sizeBytes
}

// reserve counts the candidate's size on the first root of order it fits on, and returns that root
// and how to give it back when the add fails (decisions/154). A torrent already counted — in the
// session, or reserved by another episode of the same pack — fits for free on the root it went to:
// adding it again writes nothing new.
func (b *diskBudget) reserve(configs *files.Config, tr nyaa.TorrentResult, order []files.DownloadRoot) (root files.DownloadRoot, undo func(), err error) {
	if order == nil {
		order = availableRoots(configs)
	}
	if len(order) == 0 {
		return files.DownloadRoot{}, nil, errNoDownloadRoot
	}
	if b == nil {
		for _, r := range order {
			if err = checkRootBudgetSpace(configs, r.Path, tr.Size, 0, 0, 0, 0); err == nil {
				return r, func() {}, nil
			}
		}
		return files.DownloadRoot{}, nil, err
	}
	key := budgetKey(tr.MagnetLink)

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.counted[key] {
		if r, ok := b.countedRoot[key]; ok {
			return r, func() {}, nil
		}
		return order[0], func() {}, nil
	}
	for _, r := range order {
		if err = b.checkRootLocked(configs, r.Path, tr.Size); err == nil {
			root = r
			break
		}
	}
	if err != nil {
		return files.DownloadRoot{}, nil, err
	}
	other := root.Path != b.library && b.library != ""
	b.counted[key] = true
	if b.countedRoot == nil {
		b.countedRoot = map[string]files.DownloadRoot{}
	}
	if b.byRoot == nil {
		b.byRoot = map[string]int64{}
	}
	b.countedRoot[key] = root
	b.reserved += tr.Size
	b.staged += tr.Size
	if other {
		if b.stagedDLByRoot == nil {
			b.stagedDLByRoot = map[string]int64{}
		}
		b.byRoot[root.Path] += tr.Size
		b.stagedDLByRoot[root.Path] += tr.Size
	}
	return root, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.counted, key)
		delete(b.countedRoot, key)
		b.reserved -= tr.Size
		b.staged -= tr.Size
		if other {
			b.byRoot[root.Path] -= tr.Size
			b.stagedDLByRoot[root.Path] -= tr.Size
		}
	}, nil
}

// counts reports whether one of the candidates is already paid for. The empty-disk check before the
// candidate loop must let those through: the second episode of a pack that just took the last free
// bytes still has to be recorded.
func (b *diskBudget) counts(candidates []nyaa.TorrentResult) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, tr := range candidates {
		if b.counted[budgetKey(tr.MagnetLink)] {
			return true
		}
	}
	return false
}

// errNoDownloadRoot is the add refused because no download root is there (every drive unmounted):
// a disk problem for the pass, like a full one.
var errNoDownloadRoot = fmt.Errorf("%w: no download folder is available", ErrInsufficientDiskSpace)

// budgetKey is the hash the magnet will get in the session, or the magnet itself when it doesn't
// parse — then Add fails anyway, and the key only has to be stable.
func budgetKey(magnet string) string {
	if h, err := torrents.InfoHash(magnet); err == nil {
		return h
	}
	return magnet
}
