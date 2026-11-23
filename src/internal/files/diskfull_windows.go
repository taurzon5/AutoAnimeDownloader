//go:build windows

package files

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isDiskFull reports whether err is a write refused for lack of space.
func isDiskFull(err error) bool {
	return errors.Is(err, windows.ERROR_DISK_FULL) || errors.Is(err, windows.ERROR_HANDLE_DISK_FULL)
}
