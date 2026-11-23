//go:build windows

package files

import "golang.org/x/sys/windows"

var diskFullErrno error = windows.ERROR_DISK_FULL
