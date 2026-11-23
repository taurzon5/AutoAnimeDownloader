//go:build linux || darwin

package files

import "syscall"

// SameFilesystem reports whether a and b live on the same filesystem. An error on either side
// answers false: the callers treat "separate" as the cautious case (two disk checks instead of one).
func SameFilesystem(a, b string) bool {
	var sa, sb syscall.Stat_t
	if syscall.Stat(a, &sa) != nil || syscall.Stat(b, &sb) != nil {
		return false
	}
	return sa.Dev == sb.Dev
}
