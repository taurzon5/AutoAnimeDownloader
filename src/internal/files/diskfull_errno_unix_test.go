//go:build !windows

package files

import "syscall"

var diskFullErrno error = syscall.ENOSPC
