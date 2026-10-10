//go:build !windows

package diagnosticsapi

import "syscall"

const noFollowFlag = syscall.O_NOFOLLOW
const directoryFlags = syscall.O_DIRECTORY | syscall.O_NOFOLLOW

func pidLiveness(pid int) string {
	err := syscall.Kill(pid, 0)
	if err == nil || err == syscall.EPERM {
		return "alive"
	}
	if err == syscall.ESRCH {
		return "dead"
	}
	return "unknown"
}
