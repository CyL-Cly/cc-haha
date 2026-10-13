//go:build !darwin && !linux && !windows

package clibootstrap

import "os"

func nativeResumeBirthMillis(string, os.FileInfo) float64 { return 0 }
