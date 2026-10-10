//go:build !windows

package pluginzip

import (
	"errors"
	"os"
	"strconv"
	"syscall"
)

func directoryIdentity(_ string, info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Dev == 0 && stat.Ino == 0 {
		return ""
	}
	return strconv.FormatUint(uint64(stat.Dev), 10) + ":" + strconv.FormatUint(uint64(stat.Ino), 10)
}
func unixMode(info os.FileInfo) uint16 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint16(stat.Mode)
	}
	return uint16(info.Mode().Perm()) | 0100000
}
func platformIOError(err error) (string, string) {
	switch {
	case errors.Is(err, syscall.ENOTDIR):
		return "ENOTDIR", "not a directory"
	case errors.Is(err, syscall.EISDIR):
		return "EISDIR", "illegal operation on a directory"
	case errors.Is(err, syscall.ENOSPC):
		return "ENOSPC", "no space left on device"
	case errors.Is(err, syscall.ELOOP):
		return "ELOOP", "too many symbolic links encountered"
	case errors.Is(err, syscall.EXDEV):
		return "EXDEV", "cross-device link not permitted"
	case errors.Is(err, syscall.EINVAL):
		return "EINVAL", "invalid argument"
	case errors.Is(err, syscall.EPERM):
		return "EPERM", "operation not permitted"
	}
	return "", ""
}
