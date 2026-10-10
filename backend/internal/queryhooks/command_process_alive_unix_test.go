//go:build !windows

package queryhooks

import (
	"syscall"
	"time"
)

func hookProcessAlive(pid int) bool {
	for i := 0; i < 200; i++ {
		if syscall.Kill(pid, 0) != nil {
			return false
		}
		time.Sleep(time.Millisecond)
	}
	return true
}
