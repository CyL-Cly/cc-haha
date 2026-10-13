//go:build darwin

package clibootstrap

import (
	"math"
	"os"
	"syscall"
)

func nativeResumeBirthMillis(_ string, info os.FileInfo) float64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return math.Trunc(float64(stat.Birthtimespec.Sec)*1000 + float64(stat.Birthtimespec.Nsec)/1e6)
	}
	return 0
}
