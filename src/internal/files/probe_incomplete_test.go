package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProbeIncompleteFolder(t *testing.T) {
	lib := t.TempDir()
	ok := t.TempDir()
	if err := ProbeIncompleteFolder(ok, lib); err != nil {
		t.Fatalf("an existing writable folder apart from the library must pass: %v", err)
	}
	if entries, _ := os.ReadDir(ok); len(entries) != 0 {
		t.Error("the probe must leave nothing behind")
	}

	missing := filepath.Join(t.TempDir(), "unmounted")
	if err := ProbeIncompleteFolder(missing, lib); err == nil {
		t.Error("a missing folder must fail")
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("the probe must never create the folder")
	}

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0644); err != nil {
		t.Fatal(err)
	}
	inLib := filepath.Join(lib, "incomplete")
	if err := os.Mkdir(inLib, 0755); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{
		"relative":            "incomplete",
		"a file":              file,
		"inside the library":  inLib,
		"the library itself":  lib,
		"holding the library": filepath.Dir(lib),
	} {
		if err := ProbeIncompleteFolder(dir, lib); err == nil {
			t.Errorf("%s must fail", name)
		}
	}
}

func TestSameFilesystem(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if !SameFilesystem(a, b) {
		t.Error("two temp folders share a filesystem")
	}
	if SameFilesystem(a, filepath.Join(a, "missing")) {
		t.Error("an unreadable side must answer false (the cautious case)")
	}
}

func TestIsDiskFull(t *testing.T) {
	if !isDiskFull(&os.PathError{Op: "write", Path: "x", Err: diskFullErrno}) {
		t.Error("a write refused for lack of space must count as disk full")
	}
	if isDiskFull(os.ErrPermission) {
		t.Error("a permission error is not a full disk")
	}
}
