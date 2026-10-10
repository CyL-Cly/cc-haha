package server

import (
	"os"
	"syscall"
	"time"
)

func birthtime(_ string, info os.FileInfo) time.Time {
	stat := info.Sys().(*syscall.Stat_t)
	return time.Unix(stat.Birthtimespec.Sec, stat.Birthtimespec.Nsec)
}
