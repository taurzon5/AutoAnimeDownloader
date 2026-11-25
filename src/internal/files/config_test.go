package files

import (
	"os"
	"path/filepath"
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
