package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/torrents"
)

// stagingConfig is directConfig with an incomplete folder (decisions/141).
func stagingConfig(t *testing.T) (*files.Config, string) {
	t.Helper()
	configs := directConfig(t.TempDir())
	configs.IncompletePath = t.TempDir()
	configs.MinFreeDiskGB = 0
	configs.IncompleteMinFreeDiskGB = 0
	return configs, configs.IncompletePath
}

func stubSameFilesystem(t *testing.T, same bool) {
	t.Helper()
	prev := sameFilesystem
	sameFilesystem = func(string, string) bool { return same }
	t.Cleanup(func() { sameFilesystem = prev })
}

// On another drive the incomplete folder has its own floor, and the library also counts what
// staging torrents hold and have not moved yet. On the library's own drive neither applies: the
// move is a hard link.
func TestCheckRootBudgetSpace_TwoVolumes(t *testing.T) {
	configs, staging := stagingConfig(t)

	configs.IncompleteMinFreeDiskGB = 1 << 30 // no disk has this much free
	stubSameFilesystem(t, false)
	if err := checkRootBudgetSpace(configs, configs.CompletedAnimePath, 0, 0, 0, 0, 0); !errors.Is(err, ErrInsufficientDiskSpace) || !strings.Contains(err.Error(), staging) {
		t.Fatalf("the incomplete folder's floor must refuse, naming its folder, got %v", err)
	}
	stubSameFilesystem(t, true)
	if err := checkRootBudgetSpace(configs, configs.CompletedAnimePath, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("on the library's drive the incomplete folder's floor does not apply, got %v", err)
	}

	configs.IncompleteMinFreeDiskGB = 0
	const huge = int64(1) << 62
	stubSameFilesystem(t, false)
	if err := checkRootBudgetSpace(configs, configs.CompletedAnimePath, 0, 0, 0, huge, 0); !errors.Is(err, ErrInsufficientDiskSpace) || !strings.Contains(err.Error(), configs.CompletedAnimePath) {
		t.Fatalf("what staging torrents still have to move must count on the library, got %v", err)
	}
	stubSameFilesystem(t, true)
	if err := checkRootBudgetSpace(configs, configs.CompletedAnimePath, 0, 0, 0, huge, 0); err != nil {
		t.Fatalf("on one drive nothing more lands on the library, got %v", err)
	}

	// On one drive, what staging torrents bound for other roots still download lands here first.
	stubSameFilesystem(t, true)
	if err := checkRootBudgetSpace(configs, configs.CompletedAnimePath, 0, 0, 0, 0, huge); !errors.Is(err, ErrInsufficientDiskSpace) {
		t.Fatalf("other roots' staging downloads must count on the incomplete folder's drive, got %v", err)
	}

	configs.IncompletePath = ""
	stubSameFilesystem(t, false)
	if err := checkRootBudgetSpace(configs, configs.CompletedAnimePath, 0, 0, huge, huge, 0); err != nil {
		t.Fatalf("without an incomplete folder only the library counts, got %v", err)
	}
}

func TestNewDiskBudget_CountsStagedTorrents(t *testing.T) {
	session := []torrents.TorrentInfo{
		{Hash: "a", Staged: true, BytesTotal: 100, BytesCompleted: 40},
		{Hash: "b", Staged: true, Completed: true, BytesTotal: 50, BytesCompleted: 50},
		{Hash: "c", BytesTotal: 30, BytesCompleted: 10},
		{Hash: "d", Completed: true, BytesTotal: 70, BytesCompleted: 70},
	}
	b := newDiskBudget(session, nil)
	if b.reserved != 80 || b.staged != 60 || b.stagedPending != 90 {
		t.Fatalf("reserved/staged/stagedPending = %d/%d/%d, want 80/60/90", b.reserved, b.staged, b.stagedPending)
	}
}

func TestRegisterPlacement_FreezesTheIncompleteFolder(t *testing.T) {
	configs, staging := stagingConfig(t)
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = configs.CompletedAnimePath
	placedOnDisk(t, backend, configs, directHash, 1)
	if p, _ := backend.Placement(directHash); p.IncompleteRoot != staging {
		t.Fatalf("the placement must freeze the incomplete folder, got %q", p.IncompleteRoot)
	}
}

func TestHandleTorrentFailure_LeavesAStagingWaitAlone(t *testing.T) {
	backend := torrents.NewFakeBackend()
	if _, err := backend.Add(directMagnet); err != nil {
		t.Fatal(err)
	}
	fm := &failingFM{}
	HandleTorrentFailure(directHash, fmt.Errorf("file allocation error: %w", torrents.ErrIncompleteFolderUnavailable), backend, fm)
	if _, ok := backend.Get(directHash); !ok {
		t.Error("a torrent waiting for its incomplete folder must stay in the session")
	}
	if _, removed := backend.RemovedKeepData[directHash]; removed {
		t.Error("it must not be dropped: a re-add would fail the same way")
	}
}

// The queue restarts unfinished torrents on its own; a FINISHED one that stopped for a missing
// incomplete folder is restarted by the pass once the folder is back.
func TestResumeTorrentsWaitingForStaging(t *testing.T) {
	configs, staging := stagingConfig(t)
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = configs.CompletedAnimePath
	const waiting, gone, other = "a000000000000000000000000000000000000001", "b000000000000000000000000000000000000002", "c000000000000000000000000000000000000003"
	for _, h := range []string{waiting, gone, other} {
		root := staging
		if h == gone {
			root = filepath.Join(t.TempDir(), "unmounted")
		}
		if err := backend.SetPlacement(torrents.PlacementRequest{Hash: h, SeriesDir: "Show", Kind: torrents.PlacementEpisode, Season: 1, IncompleteRoot: root}); err != nil {
			t.Fatal(err)
		}
		backend.AddPaused(h, h, 4, 4, true)
	}
	unavailable := fmt.Errorf("file allocation error: %w", torrents.ErrIncompleteFolderUnavailable)
	backend.SetStopError(waiting, unavailable)
	backend.SetStopError(gone, unavailable)
	backend.SetStopError(other, errors.New("something else"))

	resumeTorrentsWaitingForStaging(backend)

	status := func(h string) string { ti, _ := backend.Get(h); return ti.Status }
	if status(waiting) == "stopped" {
		t.Error("a torrent whose incomplete folder is back must restart")
	}
	if status(gone) != "stopped" {
		t.Error("a torrent whose incomplete folder is still gone must wait")
	}
	if status(other) != "stopped" {
		t.Error("a torrent stopped for another reason must be left alone")
	}
}

func TestPruneStagingLeftovers(t *testing.T) {
	configs, staging := stagingConfig(t)
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = configs.CompletedAnimePath
	const (
		live   = "a000000000000000000000000000000000000001"
		done   = "b000000000000000000000000000000000000002"
		kept   = "c000000000000000000000000000000000000003"
		orphan = "d000000000000000000000000000000000000004"
		young  = "e000000000000000000000000000000000000005"
		moved  = "f000000000000000000000000000000000000006"
	)
	for _, h := range []string{live, done, kept, moved} {
		root := staging
		if h == moved { // registered under the folder's old place: the user moved it here
			root = filepath.Join(t.TempDir(), "old")
		}
		if err := backend.SetPlacement(torrents.PlacementRequest{Hash: h, SeriesDir: "Show", Kind: torrents.PlacementEpisode, Season: 1, IncompleteRoot: root}); err != nil {
			t.Fatal(err)
		}
	}
	if err := backend.FinalizePlacement(done); err != nil {
		t.Fatal(err)
	}
	if err := backend.SetPlacementKept(kept, true); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * orphanPlacementGrace)
	for _, name := range []string{live, done, kept, orphan, young, moved, "Show"} {
		dir := filepath.Join(staging, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if name != young {
			if err := os.Chtimes(dir, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}

	pruneStagingLeftovers(backend, staging, time.Now())

	for name, want := range map[string]bool{live: true, done: false, kept: true, orphan: false, young: true, moved: true, "Show": true} {
		_, err := os.Stat(filepath.Join(staging, name))
		if exists := err == nil; exists != want {
			t.Errorf("%s: exists = %v, want %v", name, exists, want)
		}
	}
}

// The user moved the incomplete folder and changed incomplete_path: an unfinished torrent whose
// files are in the new place follows them; one whose files are nowhere waits (decisions/141).
func TestAdoptMovedStaging(t *testing.T) {
	configs, staging := stagingConfig(t)
	backend := torrents.NewFakeBackend()
	backend.LibraryRoot = configs.CompletedAnimePath
	const found, lost, finished = "a000000000000000000000000000000000000001", "b000000000000000000000000000000000000002", "c000000000000000000000000000000000000003"
	oldRoot := filepath.Join(t.TempDir(), "old")
	for _, h := range []string{found, lost, finished} {
		if err := backend.SetPlacement(torrents.PlacementRequest{Hash: h, SeriesDir: "Show", Kind: torrents.PlacementEpisode, Season: 1, IncompleteRoot: oldRoot}); err != nil {
			t.Fatal(err)
		}
	}
	if err := backend.FinalizePlacement(finished); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{found, finished} {
		if err := os.MkdirAll(filepath.Join(staging, h), 0755); err != nil {
			t.Fatal(err)
		}
	}

	adoptMovedStaging(backend, staging)

	root := func(h string) string { p, _ := backend.Placement(h); return p.IncompleteRoot }
	if root(found) != staging {
		t.Errorf("a download found in the moved folder must follow it, root = %q", root(found))
	}
	if root(lost) != oldRoot {
		t.Errorf("a download found nowhere must keep its root and wait, root = %q", root(lost))
	}
	if root(finished) != oldRoot {
		t.Errorf("a finished torrent has nothing to follow, root = %q", root(finished))
	}
}

func TestDiskAlertVolume_FollowsTheTighterDrive(t *testing.T) {
	configs, staging := stagingConfig(t)
	configs.MinFreeDiskGB, configs.IncompleteMinFreeDiskGB = 10, 10
	prev := diskSpace
	t.Cleanup(func() { diskSpace = prev })
	free := map[string]uint64{configs.CompletedAnimePath: 500 * uint64(giB), staging: 12 * uint64(giB)}
	diskSpace = func(path string) (uint64, uint64, error) { return 1000 * uint64(giB), free[path], nil }

	stubSameFilesystem(t, false)
	if path, floor, _, _, _ := diskAlertVolume(configs); path != staging || floor != 10 {
		t.Errorf("the nearly full incomplete drive must be the one reported, got %s (floor %d)", path, floor)
	}
	free[staging] = 900 * uint64(giB)
	if path, _, _, _, _ := diskAlertVolume(configs); path != configs.CompletedAnimePath {
		t.Errorf("with room on the incomplete drive the library is reported, got %s", path)
	}
	free[staging] = 1
	stubSameFilesystem(t, true)
	if path, _, _, _, _ := diskAlertVolume(configs); path != configs.CompletedAnimePath {
		t.Errorf("on one drive there is only the library to report, got %s", path)
	}
}
