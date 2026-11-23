//go:build windows

package files

import (
	"path/filepath"
	"strings"
)

// SameFilesystem reports whether a and b live on the same volume. An error answers false: the
// callers treat "separate" as the cautious case (two disk checks instead of one).
func SameFilesystem(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	return strings.EqualFold(filepath.VolumeName(absA), filepath.VolumeName(absB))
}
