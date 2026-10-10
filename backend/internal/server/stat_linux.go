package server

import (
	"golang.org/x/sys/unix"
	"os"
	"time"
)

func birthtime(path string, _ os.FileInfo) time.Time {
	var stat unix.Statx_t
	if unix.Statx(unix.AT_FDCWD, path, 0, unix.STATX_BTIME, &stat) == nil && stat.Mask&unix.STATX_BTIME != 0 {
		return time.Unix(stat.Btime.Sec, int64(stat.Btime.Nsec))
	}
	return time.Unix(0, 0)
}
