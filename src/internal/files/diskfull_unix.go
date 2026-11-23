//go:build !windows

package files

import (
	"errors"
	"syscall"
)

// isDiskFull reports whether err is a write refused for lack of space (or quota).
func isDiskFull(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}
