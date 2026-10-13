//go:build !windows

package server

import (
	"errors"
	"syscall"
)

func migrationProcessAlive(pid int) bool {
	// Node rejects PIDs outside the signed native range; the reference treats
	// that non-ESRCH failure conservatively as alive rather than truncating it.
	if pid > 2147483647 {
		return true
	}
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
