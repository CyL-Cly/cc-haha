//go:build windows

package autodream

import (
	"os"
	"syscall"
	"time"
)

func accessTime(path string, info os.FileInfo) time.Time {
	if stat, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return time.Unix(0, stat.LastAccessTime.Nanoseconds())
	}
	return info.ModTime()
}
