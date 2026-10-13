//go:build windows

package clibootstrap

import (
	"math"
	"os"
	"syscall"
)

func nativeResumeBirthMillis(_ string, info os.FileInfo) float64 {
	if stat, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return math.Trunc(float64(stat.CreationTime.Nanoseconds()) / 1e6)
	}
	return 0
}
