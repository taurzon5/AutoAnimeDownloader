package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigDownloadPath(t *testing.T) {
	t.Run("derives the download directory from the library", func(t *testing.T) {
		c := &Config{CompletedAnimePath: filepath.Join("/mnt", "hd", "animes")}
		want := filepath.Join("/mnt", "hd", "animes", ".torrents")
		if got := c.DownloadPath(); got != want {
			t.Errorf("DownloadPath() = %q, want %q", got, want)
		}
	})

	// Critical guard: without it filepath.Join would return the relative path
	// ".torrents" and the rain session would be created in the process
	// working directory. With "", SessionManager.Ensure returns
	// ErrSessionNotReady, which is the current behavior for incomplete config.
	t.Run("empty library returns empty string", func(t *testing.T) {
		c := &Config{CompletedAnimePath: ""}
		if got := c.DownloadPath(); got != "" {
			t.Errorf("DownloadPath() = %q, want empty string", got)
		}
	})
}

// Download Full Megapacks starts on, and Shoko sees the app's paths as they are
// (decisions/149, decisions/152).
func TestShokoPackAndPathMapDefaults(t *testing.T) {
	c := DefaultConfig()
	if !c.ShokoFullPacks {
		t.Error("shoko_full_packs should default to on")
	}
	if c.ShokoPathMap == nil || len(c.ShokoPathMap) != 0 {
		t.Errorf("shoko_path_map should default to an empty list, got %#v", c.ShokoPathMap)
	}
	if got := c.ShokoPath("/mnt/anime/Show"); got != "/mnt/anime/Show" {
		t.Errorf("no map leaves the path as it is, got %q", got)
	}
}

// shoko_path_map translates through the longest matching prefix, both ways, and a folder is
// Shoko's when the path is it or lies inside it, whatever the separator (decisions/152).
func TestShokoPathMap(t *testing.T) {
	c := DefaultConfig()
	c.ShokoPathMap = []ShokoPathMapping{
		{App: "/mnt/media", Shoko: "/data"},
		{App: "/mnt/media/anime/", Shoko: "/import"},
		{App: "/mnt/win", Shoko: `D:\Anime`},
	}
	for app, want := range map[string]string{
		"/mnt/media/anime/Show": "/import/Show", // the longer prefix wins
		"/mnt/media/anime":      "/import",
		"/mnt/media/movies":     "/data/movies",
		"/mnt/mediax":           "/mnt/mediax", // a prefix of the name, not a folder
		"/srv/other":            "/srv/other",
		"/mnt/win/Show":         `D:\Anime/Show`,
	} {
		if got := c.ShokoPath(app); got != want {
			t.Errorf("ShokoPath(%q) = %q, want %q", app, got, want)
		}
	}
	if got := c.AppPathFromShoko("/import/"); got != "/mnt/media/anime" {
		t.Errorf("AppPathFromShoko(/import/) = %q", got)
	}
	if got := c.AppPathFromShoko(`D:\Anime\Show`); got != `/mnt/win\Show` {
		t.Errorf("AppPathFromShoko(windows) = %q", got)
	}
	for _, tc := range []struct {
		path, folder string
		want         bool
	}{
		{"/import", "/import/", true},
		{"/import/Show", "/import", true},
		{"/importer", "/import", false},
		{"/", "/import", false},
		{"", "/import", false},
		{"/import", "", false},
		{`D:\Anime/Show`, `D:\Anime\`, true},
	} {
		if got := UnderFolder(tc.path, tc.folder); got != tc.want {
			t.Errorf("UnderFolder(%q, %q) = %v, want %v", tc.path, tc.folder, got, tc.want)
		}
	}
}

// A config.json from before recent_shelf_days reads it as 3: LoadConfigs unmarshals over
// DefaultConfig().
func TestRecentShelfDaysDefaultsToThree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"check_interval": 10}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := newTestManagerIn(t, dir).LoadConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if c.RecentShelfDays != 3 {
		t.Errorf("recent_shelf_days %d, want 3", c.RecentShelfDays)
	}
}

// TestRetryLimitZeroBecomesOne: an older version saved episode_retry_limit 0, which tried no magnet
// at all and is refused now; left on disk it would make every later save fail. It loads as 1 and is
// written back (decisions/090).
func TestRetryLimitZeroBecomesOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"check_interval": 10, "episode_retry_limit": 0}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := newTestManagerIn(t, dir).LoadConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if c.EpisodeRetryLimit != 1 {
		t.Errorf("EpisodeRetryLimit = %d, want 1", c.EpisodeRetryLimit)
	}
	saved, _ := os.ReadFile(path)
	if !strings.Contains(string(saved), `"episode_retry_limit": 1`) {
		t.Errorf("the 1 was not written back: %s", saved)
	}
}

// An empty library_mode in config.json means direct, the default (decisions/159), and a config
// with the mode saved keeps it.
func TestEmptyLibraryModeLoadsAsDirect(t *testing.T) {
	for _, tc := range []struct{ json, want string }{
		{`{"check_interval": 10}`, LibraryModeDirect},
		{`{"check_interval": 10, "library_mode": ""}`, LibraryModeDirect},
		{`{"check_interval": 10, "library_mode": "hardlink"}`, LibraryModeHardlink},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(tc.json), 0o644); err != nil {
			t.Fatal(err)
		}
		c, err := newTestManagerIn(t, dir).LoadConfigs()
		if err != nil {
			t.Fatal(err)
		}
		if c.LibraryMode != tc.want {
			t.Errorf("%s: LibraryMode = %q, want %q", tc.json, c.LibraryMode, tc.want)
		}
	}
}
