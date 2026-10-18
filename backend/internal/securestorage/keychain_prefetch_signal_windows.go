//go:build windows

package securestorage

import "os"

// Node's SIGTERM compatibility on Windows terminates the direct process.
func terminateKeychainPrefetchChild(process *os.Process) error {
	return process.Kill()
}
