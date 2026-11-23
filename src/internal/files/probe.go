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

// ProbeIncompleteFolder checks incomplete_path (decisions/141). Unlike ProbeWritable it never
// creates the folder: when the drive holding it is not mounted, its mount point is an empty
// directory on the system disk, and creating the folder there would quietly fill that disk. It must
// be absolute, an existing directory, writable, and apart from the library: inside the library
// the media server would scan half-downloaded files, and a library inside it would be moved into
// itself. library is completed_anime_path; "" skips that last check.
func ProbeIncompleteFolder(dir, library string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("the incomplete downloads folder must be an absolute path: %q", dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("the incomplete downloads folder %s does not exist or is not reachable (is its drive mounted?): %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("the incomplete downloads folder %s is not a folder", dir)
	}
	if library != "" {
		if inside(dir, library) || inside(library, dir) {
			return fmt.Errorf("the incomplete downloads folder %s must be outside the library %s, and the library outside it", dir, library)
		}
	}
	probe := filepath.Join(dir, ".aad_write_probe")
	if err := os.WriteFile(probe, []byte("aad"), 0644); err != nil {
		return fmt.Errorf("the incomplete downloads folder %s is not writable: %w", dir, err)
	}
	_ = os.Remove(probe)
	return nil
}

// inside reports whether path is base or below it.
func inside(path, base string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(path))
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}
