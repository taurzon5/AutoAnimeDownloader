package files

import (
	"fmt"
	"os"
	"path/filepath"
)

// ProbeWritable is direct library mode's replacement for Librarian.ProbePath: direct mode
// links nothing, so any filesystem it can create a file on works (exFAT, FAT32, SMB/NFS
// shares without hardlinks included). It creates the folder if missing, writes a small file
// and removes it.
func ProbeWritable(dir string) error {
	if dir == "" {
		return fmt.Errorf("completed anime path is not configured")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("cannot create the library folder %s: %w", dir, err)
	}
	probe := filepath.Join(dir, ".aad_write_probe")
	if err := os.WriteFile(probe, []byte("aad"), 0644); err != nil {
		return fmt.Errorf("the library folder %s is not writable: %w", dir, err)
	}
	_ = os.Remove(probe)
	return nil
}
