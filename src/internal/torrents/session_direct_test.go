package torrents

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cenkalti/rain/v2/torrent"
)

// End to end with two real rain sessions on loopback: a seeder with rain's own storage and a
// direct-mode downloader with a placement. Pins that a placed torrent is written at its library
// path (as .part), renamed by FinalizePlacement, keeps seeding from the final name after a
// session rebuild, and that removing it deletes exactly its files.

// bencode is the minimal encoder a .torrent needs: ints, strings, lists, dicts (sorted keys).
func bencode(buf *bytes.Buffer, v any) {
	switch x := v.(type) {
	case int:
		fmt.Fprintf(buf, "i%de", x)
	case int64:
		fmt.Fprintf(buf, "i%de", x)
	case string:
		fmt.Fprintf(buf, "%d:%s", len(x), x)
	case []byte:
		fmt.Fprintf(buf, "%d:", len(x))
		buf.Write(x)
	case []any:
		buf.WriteByte('l')
		for _, e := range x {
			bencode(buf, e)
		}
		buf.WriteByte('e')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('d')
		for _, k := range keys {
			bencode(buf, k)
			bencode(buf, x[k])
		}
		buf.WriteByte('e')
	default:
		panic(fmt.Sprintf("bencode: %T", v))
	}
}

type testFile struct {
	path []string // inside the torrent folder
	data []byte
}

// makeTorrent builds a multi-file .torrent (name/<path>) and returns it with its info hash.
func makeTorrent(t *testing.T, name string, files []testFile) ([]byte, string) {
	t.Helper()
	const pieceLen = 16 * 1024
	var all []byte
	var list []any
	for _, f := range files {
		all = append(all, f.data...)
		path := make([]any, len(f.path))
		for i, p := range f.path {
			path[i] = p
		}
		list = append(list, map[string]any{"length": len(f.data), "path": path})
	}
	var pieces []byte
	for off := 0; off < len(all); off += pieceLen {
		sum := sha1.Sum(all[off:min(off+pieceLen, len(all))])
		pieces = append(pieces, sum[:]...)
	}
	info := map[string]any{"name": name, "piece length": pieceLen, "pieces": pieces, "files": list}
	var infoBuf bytes.Buffer
	bencode(&infoBuf, info)
	hash := sha1.Sum(infoBuf.Bytes())

	var out bytes.Buffer
	out.WriteString("d4:info")
	out.Write(infoBuf.Bytes())
	out.WriteString("e")
	return out.Bytes(), hex.EncodeToString(hash[:])
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func waitFor(t *testing.T, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestDirectMode_RealTransferLandsInTheLibrary(t *testing.T) {
	if testing.Short() {
		t.Skip("opens two real torrent sessions")
	}
	video := randomBytes(t, 70*1024)
	subs := randomBytes(t, 3*1024)
	metainfo, hash := makeTorrent(t, "[Grp] Show - 05", []testFile{
		{path: []string{"[Grp] Show - 05.mkv"}, data: video},
		{path: []string{"[Grp] Show - 05.ass"}, data: subs},
	})

	// Seeder: rain's own storage, data already at <DataDir>/<id>/<name>/...
	seedDir := t.TempDir()
	content := filepath.Join(seedDir, hash, "[Grp] Show - 05")
	if err := os.MkdirAll(content, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, "[Grp] Show - 05.mkv"), video, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, "[Grp] Show - 05.ass"), subs, 0644); err != nil {
		t.Fatal(err)
	}
	seeder, err := newSession(seedDir, filepath.Join(t.TempDir(), "seed.db"), sessionOptions{disableDHT: true})
	if err != nil {
		t.Fatal(err)
	}
	defer seeder.Close()
	st, err := seeder.ses.AddTorrent(bytes.NewReader(metainfo), &torrent.AddTorrentOptions{ID: hash})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the seeder to verify its data", 30*time.Second, func() bool { return st.Stats().Status == torrent.Seeding })

	// Downloader: direct mode with a placement registered before the add.
	lib := t.TempDir()
	dataDir := t.TempDir()
	store := newPlacementStore(filepath.Join(dataDir, placementsFileName))
	req := PlacementRequest{
		Hash: hash, SeriesDir: "Show", SeriesName: "Show", SeriesID: 1,
		Kind: PlacementEpisode, Season: 2, RenameVideo: "Show - S02E05", PartFiles: true,
	}
	if _, err := store.register(req); err != nil {
		t.Fatal(err)
	}
	opts := sessionOptions{disableDHT: true, placements: store}
	leech, err := newSession(filepath.Join(lib, ".torrents"), filepath.Join(dataDir, "session.db"), opts)
	if err != nil {
		t.Fatal(err)
	}
	lt, err := leech.ses.AddTorrent(bytes.NewReader(metainfo), &torrent.AddTorrentOptions{ID: hash})
	if err != nil {
		t.Fatal(err)
	}
	if err := lt.AddPeer(fmt.Sprintf("127.0.0.1:%d", st.Port())); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the download to complete", 60*time.Second, func() bool { return completedFromStats(lt.Stats()) })

	final := filepath.Join(lib, "Show", "Season 02", "Show - S02E05.mkv")
	finalSubs := filepath.Join(lib, "Show", "Season 02", "[Grp] Show - 05.ass")
	if _, err := os.Stat(final + partSuffix); err != nil {
		t.Fatalf("the finished download must still be a .part until finalized: %v", err)
	}
	if _, err := os.Stat(filepath.Join(lib, ".torrents", hash)); !os.IsNotExist(err) {
		t.Fatalf("nothing may be written to the download folder: %v", err)
	}

	if err := leech.FinalizePlacement(hash); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(final)
	if err != nil || !bytes.Equal(got, video) {
		t.Fatalf("the video must be at its final name with the torrent's bytes (err %v)", err)
	}
	if got, err := os.ReadFile(finalSubs); err != nil || !bytes.Equal(got, subs) {
		t.Fatalf("the subtitle must keep its name next to the video (err %v)", err)
	}
	waitFor(t, "the torrent to seed again after the rename", 30*time.Second, func() bool { return lt.Stats().Status == torrent.Seeding })

	// A session rebuild (restart) resumes from the frozen map and the final names.
	if err := leech.Close(); err != nil {
		t.Fatal(err)
	}
	reloaded := newPlacementStore(filepath.Join(dataDir, placementsFileName))
	leech2, err := newSession(filepath.Join(lib, ".torrents"), filepath.Join(dataDir, "session.db"), sessionOptions{disableDHT: true, placements: reloaded})
	if err != nil {
		t.Fatal(err)
	}
	defer leech2.Close()
	lt2 := leech2.ses.GetTorrent(hash)
	if lt2 == nil {
		t.Fatal("the torrent must come back from resume")
	}
	waitFor(t, "the resumed torrent to seed", 30*time.Second, func() bool { return lt2.Stats().Status == torrent.Seeding })
	if completed := completedFromStats(lt2.Stats()); !completed {
		t.Fatal("the resumed torrent must still be complete")
	}
	if strings.HasSuffix(lt2.Dir(), ".torrents") {
		t.Fatalf("DataDir must be the series folder, got %q", lt2.Dir())
	}

	// Removing it without keepData deletes its files and the emptied folders, nothing else.
	keep := filepath.Join(lib, "Show", "Season 01", "other.mkv")
	if err := os.MkdirAll(filepath.Dir(keep), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := leech2.Remove(hash, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lib, "Show", "Season 02")); !os.IsNotExist(err) {
		t.Fatalf("the torrent's season folder must be gone: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("another season's file must survive: %v", err)
	}
	if _, ok := reloaded.get(hash); ok {
		t.Fatal("the placement must be forgotten with the data")
	}
}

// A finished direct-mode file deleted outside the app is not downloaded again on the next session
// open: the torrent stops with an error and the file stays absent (decisions/139).
func TestDirectMode_DeletedFinishedFileIsNotDownloadedAgain(t *testing.T) {
	if testing.Short() {
		t.Skip("opens real torrent sessions")
	}
	video := randomBytes(t, 40*1024)
	metainfo, hash := makeTorrent(t, "[Grp] Show - 07", []testFile{{path: []string{"[Grp] Show - 07.mkv"}, data: video}})

	lib := t.TempDir()
	dataDir := t.TempDir()
	final := filepath.Join(lib, "Show", "Season 01", "[Grp] Show - 07.mkv")
	if err := os.MkdirAll(filepath.Dir(final), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final, video, 0644); err != nil {
		t.Fatal(err)
	}
	store := newPlacementStore(filepath.Join(dataDir, placementsFileName))
	if _, err := store.register(PlacementRequest{Hash: hash, SeriesDir: "Show", Kind: PlacementEpisode, Season: 1}); err != nil {
		t.Fatal(err)
	}
	p, _ := store.get(hash)
	p.Files = []PlacedFile{{Torrent: "[Grp] Show - 07/[Grp] Show - 07.mkv", Path: "Show/Season 01/[Grp] Show - 07.mkv"}}
	p.Completed = true
	if err := store.update(p); err != nil {
		t.Fatal(err)
	}

	open := func() *Session {
		s, err := newSession(filepath.Join(lib, ".torrents"), filepath.Join(dataDir, "session.db"), sessionOptions{disableDHT: true, placements: store})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s1 := open()
	t1, err := s1.ses.AddTorrent(bytes.NewReader(metainfo), &torrent.AddTorrentOptions{ID: hash})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the existing file to verify and seed", 30*time.Second, func() bool { return t1.Stats().Status == torrent.Seeding })
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(final); err != nil {
		t.Fatal(err)
	}
	s2 := open()
	defer s2.Close()
	t2 := s2.ses.GetTorrent(hash)
	if t2 == nil {
		t.Fatal("the torrent must come back from resume")
	}
	waitFor(t, "the torrent to stop on the missing file", 30*time.Second, func() bool {
		st := t2.Stats()
		return st.Status == torrent.Stopped && st.Error != nil
	})
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Fatalf("the deleted file must not be recreated: %v", err)
	}
	if err := t2.Stats().Error; !strings.Contains(err.Error(), ErrPlacedFileMissing.Error()) {
		t.Fatalf("the torrent's error must name the missing file, got %v", err)
	}
}

// The bulk "redownload missing files" (decisions/139): a finished torrent whose file was deleted
// is fetched again into the same path, and the recreated, still-empty file never passes as the
// old finished download — not even across a restart before the first piece arrives.
func TestDirectMode_RedownloadPlacementFetchesTheDeletedFileAgain(t *testing.T) {
	if testing.Short() {
		t.Skip("opens two real torrent sessions")
	}
	video := randomBytes(t, 50*1024)
	metainfo, hash := makeTorrent(t, "[Grp] Show - 09", []testFile{{path: []string{"[Grp] Show - 09.mkv"}, data: video}})

	seedDir := t.TempDir()
	content := filepath.Join(seedDir, hash, "[Grp] Show - 09")
	if err := os.MkdirAll(content, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, "[Grp] Show - 09.mkv"), video, 0644); err != nil {
		t.Fatal(err)
	}
	seeder, err := newSession(seedDir, filepath.Join(t.TempDir(), "seed.db"), sessionOptions{disableDHT: true})
	if err != nil {
		t.Fatal(err)
	}
	defer seeder.Close()
	st, err := seeder.ses.AddTorrent(bytes.NewReader(metainfo), &torrent.AddTorrentOptions{ID: hash})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the seeder to verify", 30*time.Second, func() bool { return st.Stats().Status == torrent.Seeding })

	lib := t.TempDir()
	store := newPlacementStore(filepath.Join(t.TempDir(), placementsFileName))
	if _, err := store.register(PlacementRequest{Hash: hash, SeriesDir: "Show", Kind: PlacementEpisode, Season: 1, PartFiles: true}); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "session.db")
	leech, err := newSession(filepath.Join(lib, ".torrents"), dbPath, sessionOptions{disableDHT: true, placements: store})
	if err != nil {
		t.Fatal(err)
	}
	lt, err := leech.ses.AddTorrent(bytes.NewReader(metainfo), &torrent.AddTorrentOptions{ID: hash})
	if err != nil {
		t.Fatal(err)
	}
	peer := fmt.Sprintf("127.0.0.1:%d", st.Port())
	if err := lt.AddPeer(peer); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first download", 60*time.Second, func() bool { return lt.Stats().Status == torrent.Seeding })
	if err := leech.FinalizePlacement(hash); err != nil {
		t.Fatal(err)
	}

	final := filepath.Join(lib, "Show", "Season 01", "[Grp] Show - 09.mkv")
	if err := os.Remove(final); err != nil {
		t.Fatal(err)
	}
	if err := leech.RedownloadPlacement(hash, ""); err != nil {
		t.Fatal(err)
	}

	// No peer yet, and the daemon restarts before the first piece: the recreated, empty .part
	// must not count as the old complete download (rain's stored bitfield said "all pieces").
	settled := func(what string, tt *torrent.Torrent) {
		t.Helper()
		waitFor(t, what, 30*time.Second, func() bool {
			s := tt.Stats().Status
			return s == torrent.Downloading || s == torrent.Seeding
		})
		if ls := tt.Stats(); ls.Pieces.Have != 0 || ls.Status == torrent.Seeding {
			t.Fatalf("%s: the empty .part must not pass as finished, got status %v with %d pieces", what, ls.Status, ls.Pieces.Have)
		}
	}
	lt = leech.ses.GetTorrent(hash)
	if lt == nil {
		t.Fatal("the torrent must be back in the session")
	}
	settled("the redownload to start", lt)
	if err := leech.Close(); err != nil {
		t.Fatal(err)
	}
	leech, err = newSession(filepath.Join(lib, ".torrents"), dbPath, sessionOptions{disableDHT: true, placements: store})
	if err != nil {
		t.Fatal(err)
	}
	defer leech.Close()
	lt = leech.ses.GetTorrent(hash)
	if lt == nil {
		t.Fatal("the torrent must survive the restart")
	}
	settled("the redownload to start after a restart", lt)

	if err := lt.AddPeer(peer); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the missing file to download again", 60*time.Second, func() bool {
		data, err := os.ReadFile(final + partSuffix)
		return err == nil && bytes.Equal(data, video) && lt.Stats().Status == torrent.Seeding
	})
	if err := leech.FinalizePlacement(hash); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(final)
	if err != nil || !bytes.Equal(got, video) {
		t.Fatalf("the file must be back at the same path with the torrent's bytes (err %v)", err)
	}
}

// A bulk redownload adds the torrents back to rain, which starts each at once: the queue must
// still hold them to max_concurrent_downloads (decisions/041), like any other add.
func TestSessionManager_RedownloadPlacementRespectsTheQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a real torrent session")
	}
	m, _, _ := newTestManager(t)
	lib := t.TempDir()
	m.SetDirectMode(true)
	m.SetMaxActiveDownloads(1)
	if _, err := m.Ensure(filepath.Join(lib, ".torrents")); err != nil {
		t.Fatal(err)
	}

	var hashes, finals []string
	for _, ep := range []string{"01", "02"} {
		name := "[Grp] Show - " + ep
		video := randomBytes(t, 40*1024)
		metainfo, hash := makeTorrent(t, name, []testFile{{path: []string{name + ".mkv"}, data: video}})
		if err := m.SetPlacement(PlacementRequest{Hash: hash, SeriesDir: "Show", Kind: PlacementEpisode, Season: 1}); err != nil {
			t.Fatal(err)
		}
		// The finished file is already in the library under its planned name (the map is set here,
		// or planning would see the file and pick a collision name): rain verifies it and seeds.
		p, _ := m.placements.get(hash)
		p.Files = []PlacedFile{{Torrent: name + "/" + name + ".mkv", Path: filepath.Join("Show", "Season 01", name+".mkv")}}
		if err := m.placements.update(p); err != nil {
			t.Fatal(err)
		}
		final := filepath.Join(lib, "Show", "Season 01", name+".mkv")
		if err := os.MkdirAll(filepath.Dir(final), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(final, video, 0644); err != nil {
			t.Fatal(err)
		}
		tt, err := m.session.ses.AddTorrent(bytes.NewReader(metainfo), &torrent.AddTorrentOptions{ID: hash})
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the torrent to verify its file", 30*time.Second, func() bool { return tt.Stats().Status == torrent.Seeding })
		if err := m.FinalizePlacement(hash); err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, hash)
		finals = append(finals, final)
	}
	for _, f := range finals {
		if err := os.Remove(f); err != nil {
			t.Fatal(err)
		}
	}
	for _, h := range hashes {
		if err := m.RedownloadPlacement(h, ""); err != nil {
			t.Fatal(err)
		}
	}

	// No peers, so neither finishes: exactly one may hold the download slot.
	running := func() (active, waiting int) {
		for _, ti := range m.List() {
			switch ti.Status {
			case StatusQueued:
				waiting++
			case StatusStopped:
			default:
				active++
			}
		}
		return
	}
	waitFor(t, "the queue to settle", 30*time.Second, func() bool {
		a, w := running()
		return a == 1 && w == 1
	})
	time.Sleep(500 * time.Millisecond)
	if a, w := running(); a != 1 || w != 1 {
		t.Fatalf("max_concurrent_downloads 1: want 1 active and 1 queued, got %d and %d", a, w)
	}
}

// A staging torrent downloads into the incomplete folder by its final name, and finalizing moves
// it into the library, where it keeps seeding: a new peer downloads it from the library copy
// (decisions/141). Both the copy (another drive) and the hard link (same filesystem) paths.
func TestDirectMode_StagedDownloadMovesIntoTheLibrary(t *testing.T) {
	if testing.Short() {
		t.Skip("opens real torrent sessions")
	}
	for _, tc := range []struct {
		name  string
		links bool
	}{{"copy", false}, {"hard link", true}} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.links {
				linkFile = func(string, string) error { return errors.New("another drive") }
				t.Cleanup(func() { linkFile = os.Link })
			}
			video := randomBytes(t, 50*1024)
			name := "[Grp] Show - 03"
			metainfo, hash := makeTorrent(t, name, []testFile{{path: []string{name + ".mkv"}, data: video}})

			seedDir := t.TempDir()
			content := filepath.Join(seedDir, hash, name)
			if err := os.MkdirAll(content, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(content, name+".mkv"), video, 0644); err != nil {
				t.Fatal(err)
			}
			seeder, err := newSession(seedDir, filepath.Join(t.TempDir(), "seed.db"), sessionOptions{disableDHT: true})
			if err != nil {
				t.Fatal(err)
			}
			st, err := seeder.ses.AddTorrent(bytes.NewReader(metainfo), &torrent.AddTorrentOptions{ID: hash})
			if err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the seeder to verify", 30*time.Second, func() bool { return st.Stats().Status == torrent.Seeding })

			lib, staging := t.TempDir(), t.TempDir()
			store := newPlacementStore(filepath.Join(t.TempDir(), placementsFileName))
			req := PlacementRequest{Hash: hash, SeriesDir: "Show", Kind: PlacementEpisode, Season: 1, PartFiles: true, IncompleteRoot: staging}
			if _, err := store.register(req); err != nil {
				t.Fatal(err)
			}
			leech, err := newSession(filepath.Join(lib, ".torrents"), filepath.Join(t.TempDir(), "session.db"), sessionOptions{disableDHT: true, placements: store})
			if err != nil {
				t.Fatal(err)
			}
			defer leech.Close()
			lt, err := leech.ses.AddTorrent(bytes.NewReader(metainfo), &torrent.AddTorrentOptions{ID: hash})
			if err != nil {
				t.Fatal(err)
			}
			if err := lt.AddPeer(fmt.Sprintf("127.0.0.1:%d", st.Port())); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the download", 60*time.Second, func() bool { return lt.Stats().Status == torrent.Seeding })

			final := filepath.Join(lib, "Show", "Season 01", name+".mkv")
			staged := filepath.Join(staging, hash, "Show", "Season 01", name+".mkv")
			if got, err := os.ReadFile(staged); err != nil || !bytes.Equal(got, video) {
				t.Fatalf("the download must land in the incomplete folder by its final name (err %v)", err)
			}
			for _, path := range []string{final, final + partSuffix} {
				if _, err := os.Stat(path); err == nil {
					t.Fatalf("nothing may reach the library before the move: %s", path)
				}
			}

			if err := leech.FinalizePlacement(hash); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(final); err != nil || !bytes.Equal(got, video) {
				t.Fatalf("the file must be in the library with the torrent's bytes (err %v)", err)
			}
			if _, err := os.Stat(filepath.Join(staging, hash)); err == nil {
				t.Fatal("the staged copy must be cleaned up")
			}
			if _, err := os.Stat(final + movingSuffix); err == nil {
				t.Fatal("no .aad-moving file may stay behind")
			}
			if p, _ := store.get(hash); !p.Completed {
				t.Fatal("the placement must be completed after the move")
			}
			waitFor(t, "seeding again", 30*time.Second, func() bool { return lt.Stats().Status == torrent.Seeding })

			// The original seeder leaves; a new peer can only get the file from the library copy.
			if err := seeder.Close(); err != nil {
				t.Fatal(err)
			}
			peer2, err := newSession(t.TempDir(), filepath.Join(t.TempDir(), "peer2.db"), sessionOptions{disableDHT: true})
			if err != nil {
				t.Fatal(err)
			}
			defer peer2.Close()
			p2, err := peer2.ses.AddTorrent(bytes.NewReader(metainfo), &torrent.AddTorrentOptions{ID: hash})
			if err != nil {
				t.Fatal(err)
			}
			if err := p2.AddPeer(fmt.Sprintf("127.0.0.1:%d", lt.Port())); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "a new peer to download from the library copy", 60*time.Second, func() bool { return p2.Stats().Status == torrent.Seeding })
		})
	}
}

// Following a moved incomplete folder re-checks the moved files: a copy older than rain's saved
// progress (moved while the torrent was writing) must be repaired, not trusted (decisions/141).
func TestDirectMode_MovedStagingIsRechecked(t *testing.T) {
	if testing.Short() {
		t.Skip("opens real torrent sessions")
	}
	video := randomBytes(t, 64*1024)
	name := "[Grp] Show - 04"
	metainfo, hash := makeTorrent(t, name, []testFile{{path: []string{name + ".mkv"}, data: video}})
	seedDir := t.TempDir()
	content := filepath.Join(seedDir, hash, name)
	if err := os.MkdirAll(content, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, name+".mkv"), video, 0644); err != nil {
		t.Fatal(err)
	}
	seeder, err := newSession(seedDir, filepath.Join(t.TempDir(), "seed.db"), sessionOptions{disableDHT: true})
	if err != nil {
		t.Fatal(err)
	}
	defer seeder.Close()
	st, err := seeder.ses.AddTorrent(bytes.NewReader(metainfo), &torrent.AddTorrentOptions{ID: hash})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the seeder to verify", 30*time.Second, func() bool { return st.Stats().Status == torrent.Seeding })

	lib, oldRoot, newRoot := t.TempDir(), t.TempDir(), t.TempDir()
	store := newPlacementStore(filepath.Join(t.TempDir(), placementsFileName))
	if _, err := store.register(PlacementRequest{Hash: hash, SeriesDir: "Show", Kind: PlacementEpisode, Season: 1, IncompleteRoot: oldRoot}); err != nil {
		t.Fatal(err)
	}
	leech, err := newSession(filepath.Join(lib, ".torrents"), filepath.Join(t.TempDir(), "session.db"), sessionOptions{disableDHT: true, placements: store})
	if err != nil {
		t.Fatal(err)
	}
	defer leech.Close()
	lt, err := leech.ses.AddTorrent(bytes.NewReader(metainfo), &torrent.AddTorrentOptions{ID: hash})
	if err != nil {
		t.Fatal(err)
	}
	peer := fmt.Sprintf("127.0.0.1:%d", st.Port())
	if err := lt.AddPeer(peer); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the download", 60*time.Second, func() bool { return lt.Stats().Status == torrent.Seeding })
	if err := stopAndWait(lt); err != nil {
		t.Fatal(err)
	}

	// The user moves the folder; the copy is stale: one block never made it.
	rel := filepath.Join(hash, "Show", "Season 01", name+".mkv")
	stale := append([]byte(nil), video...)
	copy(stale[20*1024:], make([]byte, 16*1024))
	if err := os.MkdirAll(filepath.Dir(filepath.Join(newRoot, rel)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newRoot, rel), stale, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(oldRoot); err != nil {
		t.Fatal(err)
	}

	if err := leech.MovePlacementStaging(hash, newRoot); err != nil {
		t.Fatal(err)
	}
	lt = leech.ses.GetTorrent(hash)
	if lt == nil {
		t.Fatal("the torrent must be back in the session")
	}
	if err := lt.AddPeer(peer); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the stale block to be fetched again", 60*time.Second, func() bool {
		data, err := os.ReadFile(filepath.Join(newRoot, rel))
		return err == nil && bytes.Equal(data, video) && lt.Stats().Status == torrent.Seeding
	})
	if p, _ := store.get(hash); p.IncompleteRoot != newRoot {
		t.Fatalf("the placement must point at the new folder, got %q", p.IncompleteRoot)
	}
}
