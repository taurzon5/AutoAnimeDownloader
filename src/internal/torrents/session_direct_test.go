package torrents

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
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
