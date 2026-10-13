//go:build !windows

package clibootstrap

import (
	"errors"
	"syscall"
)

func nativeAutoDreamProcessRunning(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
