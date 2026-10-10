//go:build !windows

package computeruse

import (
	"net"
	"os"
	"syscall"
)

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}
func restoreNonblocking(connection *net.UnixConn) error {
	raw, err := connection.SyscallConn()
	if err != nil {
		return err
	}
	var failure error
	err = raw.Control(func(fd uintptr) { failure = syscall.SetNonblock(int(fd), true) })
	if err != nil {
		return err
	}
	return failure
}
func processAlive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }
