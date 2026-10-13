//go:build !darwin && !linux && !windows

package autodream

import (
	"os"
	"time"
)

func accessTime(path string, info os.FileInfo) time.Time { return info.ModTime() }
