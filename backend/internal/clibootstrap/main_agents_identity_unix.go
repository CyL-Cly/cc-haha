//go:build !windows

package clibootstrap

import (
	"io/fs"
	"strconv"
	"syscall"
)

func agentsFileIdentity(i fs.FileInfo) string {
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok || s.Dev == 0 && s.Ino == 0 {
		return ""
	}
	return strconv.FormatUint(uint64(s.Dev), 10) + ":" + strconv.FormatUint(uint64(s.Ino), 10)
}
