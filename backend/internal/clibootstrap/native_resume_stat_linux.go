//go:build linux

package clibootstrap

import (
	"golang.org/x/sys/unix"
	"math"
	"os"
)

func nativeResumeBirthMillis(path string, _ os.FileInfo) float64 {
	var stat unix.Statx_t
	if unix.Statx(unix.AT_FDCWD, path, 0, unix.STATX_BTIME, &stat) == nil && stat.Mask&unix.STATX_BTIME != 0 {
		return math.Trunc(float64(stat.Btime.Sec)*1000 + float64(stat.Btime.Nsec)/1e6)
	}
	return 0
}
