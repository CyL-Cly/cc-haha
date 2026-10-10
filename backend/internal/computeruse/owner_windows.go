//go:build windows

package computeruse

import (
	"errors"
	"net"
	"os"
)

func ownedByCurrentUser(info os.FileInfo) bool { return false }
func restoreNonblocking(connection *net.UnixConn) error {
	return errors.New("macOS native daemon transport is unavailable on Windows")
}
func processAlive(pid int) bool { return false }
