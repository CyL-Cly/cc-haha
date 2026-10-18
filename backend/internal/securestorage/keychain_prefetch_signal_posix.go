//go:build !windows

package securestorage

import (
	"os"
	"syscall"
)

func terminateKeychainPrefetchChild(process *os.Process) error {
	return process.Signal(syscall.SIGTERM)
}
