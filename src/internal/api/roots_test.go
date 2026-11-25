package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"AutoAnimeDownloader/src/internal/daemon"
	"AutoAnimeDownloader/src/internal/files"
)

// download_roots in direct mode: names unique and set, paths absolute, distinct, not nested and
// writable; empty is the library alone (decisions/154).
func TestUpdateConfig_ValidatesDownloadRoots(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for name, roots := range map[string][]files.DownloadRoot{
		"no name":        {{Name: " ", Path: a}},
		"same name":      {{Name: "x", Path: a}, {Name: "x", Path: b}},
		"relative path":  {{Name: "x", Path: "anime"}},
		"same folder":    {{Name: "x", Path: a}, {Name: "y", Path: a + "/"}},
		"nested":         {{Name: "x", Path: a}, {Name: "y", Path: filepath.Join(a, "inner")}},
		"nested reverse": {{Name: "y", Path: filepath.Join(b, "inner")}, {Name: "x", Path: b}},
		"not there":      {{Name: "x", Path: filepath.Join(a, "missing", "deeper")}},
		"negative limit": {{Name: "x", Path: a, MinFreeGB: func() *int { n := -1; return &n }()}},
	} {
		c := files.DefaultConfig()
		c.CompletedAnimePath = t.TempDir()
		c.LibraryMode = files.LibraryModeDirect
		c.DownloadRoots = roots
		server := &Server{State: daemon.NewState(), FileManager: &mockFileManager{configs: files.DefaultConfig()}}
		if w := putConfig(t, server, c); w.Code != http.StatusBadRequest {
			t.Errorf("%s: PUT = %d, want 400", name, w.Code)
		}
	}
	c := files.DefaultConfig()
	c.CompletedAnimePath = t.TempDir()
	c.LibraryMode = files.LibraryModeDirect
	c.DownloadRoots = []files.DownloadRoot{{Name: "ssd", Path: a}, {Name: "hdd", Path: b}}
	server := &Server{State: daemon.NewState(), FileManager: &mockFileManager{configs: files.DefaultConfig()}}
	if w := putConfig(t, server, c); w.Code != http.StatusOK {
		t.Fatalf("two good roots = %d %s", w.Code, w.Body.String())
	}
	// Hardlink mode ignores them, as it ignores every direct-mode field.
	c.LibraryMode = files.LibraryModeHardlink
	c.DownloadRoots = []files.DownloadRoot{{Name: "", Path: "relative"}}
	server.Librarian = nil
	if w := putConfig(t, server, c); w.Code != http.StatusOK {
		t.Fatalf("hardlink mode with stale roots = %d %s", w.Code, w.Body.String())
	}
}

// An anime's root override names an existing download root; "" drops it, absent keeps it.
func TestPutAnimeSettings_Root(t *testing.T) {
	server, fm := newSettingsTestServer(t)
	cfg := files.DefaultConfig()
	cfg.LibraryMode = files.LibraryModeDirect
	cfg.DownloadRoots = []files.DownloadRoot{{Name: "ssd", Path: "/mnt/ssd"}, {Name: "hdd", Path: "/mnt/hdd"}}
	fm.configs = cfg

	if rec := putSettings(t, server, 7, `{"root":"hdd"}`); rec.Code != http.StatusOK || fm.animeSettings[7].Root == nil || *fm.animeSettings[7].Root != "hdd" {
		t.Fatalf("set = %d %+v", rec.Code, fm.animeSettings[7])
	}
	if rec := putSettings(t, server, 7, `{"progress":3}`); rec.Code != http.StatusOK || fm.animeSettings[7].Root == nil {
		t.Fatal("a PUT without root dropped the override")
	}
	if rec := putSettings(t, server, 7, `{"root":"nas"}`); rec.Code != http.StatusBadRequest || *fm.animeSettings[7].Root != "hdd" {
		t.Fatalf("an unknown root = %d, want 400 and the override kept", rec.Code)
	}
	if rec := putSettings(t, server, 7, `{"root":""}`); rec.Code != http.StatusOK || fm.animeSettings[7].Root != nil {
		t.Fatalf("clear = %d %+v", rec.Code, fm.animeSettings[7])
	}
}

// GET /status lists one disk per download root: the library alone without download_roots.
func TestHandleStatus_DisksPerRoot(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	cfg := files.DefaultConfig()
	cfg.CompletedAnimePath = t.TempDir()
	for _, tc := range []struct {
		roots []files.DownloadRoot
		want  []string
	}{
		{nil, []string{files.LibraryRootName}},
		{[]files.DownloadRoot{{Name: "ssd", Path: a}, {Name: "hdd", Path: b}}, []string{"ssd", "hdd"}},
	} {
		cfg.LibraryMode = files.LibraryModeDirect
		cfg.DownloadRoots = tc.roots
		server := &Server{State: daemon.NewState(), FileManager: &mockFileManager{configs: cfg}}
		w := httptest.NewRecorder()
		handleStatus(server)(w, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
		var resp struct {
			Data StatusResponse `json:"data"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, d := range resp.Data.Disks {
			got = append(got, d.Name)
			if d.Total == 0 {
				t.Errorf("disk %s has no total", d.Name)
			}
		}
		if len(got) != len(tc.want) || (len(got) > 0 && got[0] != tc.want[0]) {
			t.Errorf("disks = %v, want %v", got, tc.want)
		}
	}
}

// With a qBittorrent path map, every download folder must be under one of its app folders: it is
// the torrent's save path there (decisions/154).
func TestUpdateConfig_DownloadRootsUnderThePathMap(t *testing.T) {
	base := t.TempDir()
	lib, mapped, outside := filepath.Join(base, "lib"), filepath.Join(base, "hdd"), t.TempDir()
	for _, dir := range []string{lib, mapped} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		root string
		want int
	}{{mapped, http.StatusOK}, {outside, http.StatusBadRequest}} {
		c := files.DefaultConfig()
		c.CompletedAnimePath = lib
		c.LibraryMode = files.LibraryModeDirect
		c.TorrentClient, c.QBittorrentURL = files.TorrentClientQBittorrent, "http://qbit:8080"
		c.QBittorrentPathMap = []files.PathMapping{{App: base, QBittorrent: "/downloads"}}
		c.DownloadRoots = []files.DownloadRoot{{Name: "lib", Path: lib}, {Name: "other", Path: tc.root}}
		server := &Server{State: daemon.NewState(), FileManager: &mockFileManager{configs: files.DefaultConfig()}}
		if w := putConfig(t, server, c); w.Code != tc.want {
			t.Errorf("root %s: PUT = %d %s, want %d", tc.root, w.Code, w.Body.String(), tc.want)
		}
	}
}
