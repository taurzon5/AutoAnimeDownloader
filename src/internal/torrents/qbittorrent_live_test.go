package torrents

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestQBitLive drives a real qBittorrent through QBitBackend: the connection test, a pack and a
// single episode added, placed, downloaded from a seeder, finalized and removed; on the way, the
// limits of decisions/144 (no share limit under a strict global one, disable_seeding's upload
// floor, the managed queue). It changes the downloader's global settings, so it is meant for the
// throwaway containers of scripts/qbittorrent-live.sh. Only runs with
// AAD_LIVE_QBITTORRENT=1 and the AAD_QBIT_* variables (docs/agents/testing.md, "qBittorrent Live
// Test"), so `go test ./...` doesn't need Docker.
func TestQBitLive(t *testing.T) {
	if os.Getenv("AAD_LIVE_QBITTORRENT") != "1" {
		t.Skip("set AAD_LIVE_QBITTORRENT=1 and the AAD_QBIT_* variables to run against a real qBittorrent")
	}
	env := func(key string) string {
		v := os.Getenv(key)
		if v == "" {
			t.Fatalf("%s is not set", key)
		}
		return v
	}
	library := env("AAD_QBIT_LIBRARY")
	seedData := env("AAD_QBIT_SEED_DATA")
	settings := QBitSettings{
		URL: env("AAD_QBIT_URL"), Username: "admin", Password: env("AAD_QBIT_PASSWORD"), Category: "aad-live",
		PathMap:        []QBitPathMapping{{App: library, QBittorrent: env("AAD_QBIT_LIBRARY_IN_QBIT")}},
		ManageSettings: true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	admin, err := newQBitClient(settings.URL, settings.Username, settings.Password, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.login(ctx); err != nil {
		t.Fatalf("login: %v", err)
	}
	// A global share limit that stops a torrent the moment it finishes (max_ratio_act 0), and
	// qBittorrent's default queue, which the backend must replace with max_concurrent_downloads.
	if _, err := admin.call(ctx, http.MethodPost, "app/setPreferences", url.Values{"json": {`{"max_ratio_enabled":true,` +
		`"max_ratio":0,"max_seeding_time_enabled":true,"max_seeding_time":0,"max_ratio_act":0,"queueing_enabled":true,` +
		`"max_active_downloads":3,"max_active_uploads":3,"max_active_torrents":5}`}}); err != nil {
		t.Fatalf("setting the strict global limits: %v", err)
	}

	res, err := TestQBittorrent(settings, library)
	if err != nil {
		t.Fatalf("connection test: %v", err)
	}
	t.Logf("qBittorrent %s, Web API %s, library seen at %s", res.AppVersion, res.WebAPIVersion, res.LibraryPath)
	if !res.LibraryVisible || res.Problem != "" {
		t.Fatalf("connection test: %+v", res)
	}

	b := NewQBitBackend(t.TempDir(), func() (QBitSettings, error) { return settings, nil })
	b.pollInterval = 500 * time.Millisecond
	defer b.Close()
	completed := make(chan string, 8)
	failed := make(chan error, 8)
	b.SetCallbacks(func(hash string) { completed <- hash }, func(hash string, err error) { failed <- err })
	b.SetMaxActiveDownloads(2)
	b.SetUploadDisabled(true)
	if _, err := b.Ensure(filepath.Join(library, ".torrents")); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if queue, err := admin.queuePrefs(ctx); err != nil || queue != qbitWantedQueue(2) {
		t.Fatalf("qBittorrent's queue after Ensure = %+v, %v; want %+v", queue, err, qbitWantedQueue(2))
	}

	packMagnet, singleMagnet := env("AAD_QBIT_PACK_MAGNET"), env("AAD_QBIT_SINGLE_MAGNET")
	packHash, _ := InfoHash(packMagnet)
	singleHash, _ := InfoHash(singleMagnet)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(b.SetPlacement(PlacementRequest{Hash: packHash, SeriesDir: "Live Show", Kind: PlacementPack, PartFiles: true}))
	must(b.SetPlacement(PlacementRequest{Hash: singleHash, SeriesDir: "Live Show", Kind: PlacementEpisode, Season: 1,
		RenameVideo: "Live Show - S01E03", PartFiles: true}))
	for _, m := range []string{packMagnet, singleMagnet} {
		if _, err := b.Add(m); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	pending := map[string]bool{packHash: true, singleHash: true}
	deadline := time.After(3 * time.Minute)
	for len(pending) > 0 {
		select {
		case h := <-completed:
			delete(pending, h)
		case err := <-failed:
			t.Fatalf("a torrent failed: %v", err)
		case <-deadline:
			t.Fatalf("not complete after 3 minutes: %v; list %+v", pending, b.List())
		}
	}
	must(b.FinalizePlacement(packHash))
	must(b.FinalizePlacement(singleHash))
	// qBittorrent applies share limits on a timer: a torrent following the global limit above was
	// stopped within 2 s of finishing when measured, so 5 s shows the app's torrents don't.
	time.Sleep(5 * time.Second)
	listed, err := admin.info(ctx, settings.Category)
	if err != nil || len(listed) != 2 {
		t.Fatalf("torrents/info = %d torrents, %v", len(listed), err)
	}
	for _, tor := range listed {
		if tor.hasShareLimit() || tor.UpLimit == nil || *tor.UpLimit != settings.uploadFloor() {
			t.Errorf("%s: share limit %v, up_limit %v; want none and the floor %d", tor.Name, tor.hasShareLimit(), tor.UpLimit, settings.uploadFloor())
		}
	}
	// disable_seeding off lifts the floor on the next polls.
	b.SetUploadDisabled(false)
	if !qbitWaitFor(10*time.Second, func() bool {
		listed, err := admin.info(ctx, settings.Category)
		for _, tor := range listed {
			if tor.UpLimit == nil || *tor.UpLimit != 0 {
				return false
			}
		}
		return err == nil && len(listed) == 2
	}) {
		t.Error("the upload floor was not lifted after disable_seeding was turned off")
	}

	want := map[string]string{
		filepath.Join(library, "Live Show", "LivePack", "Live Show - 01.mkv"):      filepath.Join(seedData, "LivePack", "Live Show - 01.mkv"),
		filepath.Join(library, "Live Show", "LivePack", "Live Show - 02.mkv"):      filepath.Join(seedData, "LivePack", "Live Show - 02.mkv"),
		filepath.Join(library, "Live Show", "LivePack", "Extras", "NCOP.mkv"):      filepath.Join(seedData, "LivePack", "Extras", "NCOP.mkv"),
		filepath.Join(library, "Live Show", "Season 01", "Live Show - S01E03.mkv"): filepath.Join(seedData, "Live Show - 03.mkv"),
	}
	for got, seed := range want {
		a, errA := os.ReadFile(got)
		bb, errB := os.ReadFile(seed)
		if errA != nil || errB != nil || !bytes.Equal(a, bb) {
			t.Errorf("%s: %v / %v, equal to the seed: %v", got, errA, errB, errA == nil && errB == nil && bytes.Equal(a, bb))
		}
		if pathExists(got + partSuffix) {
			t.Errorf("%s still has its .part twin", got)
		}
	}
	for _, h := range []string{packHash, singleHash} {
		info, ok := b.Get(h)
		if !ok || !info.Completed || info.Status != "seeding" || info.PiecesHave != qbitProgressScale {
			t.Errorf("Get(%s) = %+v, %v", h, info, ok)
		}
		if p, _ := b.Placement(h); !p.Completed {
			t.Errorf("placement %s not completed", h)
		}
	}
	files, err := b.Files(packHash)
	if err != nil || len(files) != 3 || files[0].Path != "LivePack/Live Show - 01.mkv" {
		t.Errorf("Files(pack) = %+v, %v; want the raw names", files, err)
	}

	// Removing the single keeping its data, then the pack with its files.
	must(b.Remove(singleHash, true))
	single := filepath.Join(library, "Live Show", "Season 01", "Live Show - S01E03.mkv")
	if !hasSize(single, 2<<20) {
		t.Error("keepData removed the single episode's file")
	}
	must(b.Remove(packHash, false))
	if pathExists(filepath.Join(library, "Live Show", "LivePack")) {
		t.Error("the pack's folder is still in the library")
	}
	if _, ok := b.Placement(packHash); ok {
		t.Error("the pack's placement was not forgotten")
	}
	if len(b.List()) != 0 {
		t.Errorf("List after removing both = %+v", b.List())
	}
}
