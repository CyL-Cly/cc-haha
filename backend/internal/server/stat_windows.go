package server

import (
	"os"
	"syscall"
	"time"
)

func birthtime(_ string, info os.FileInfo) time.Time {
	stat := info.Sys().(*syscall.Win32FileAttributeData)
	return time.Unix(0, stat.CreationTime.Nanoseconds())
}
