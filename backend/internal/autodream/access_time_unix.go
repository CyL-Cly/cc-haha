//go:build darwin || linux

package autodream

import (
	"golang.org/x/sys/unix"
	"os"
	"time"
)

func accessTime(path string, info os.FileInfo) time.Time {
	var stat unix.Stat_t
	if unix.Stat(path, &stat) == nil {
		return time.Unix(stat.Atim.Sec, stat.Atim.Nsec)
	}
	return info.ModTime()
}
