//go:build !windows

package clibootstrap

import (
	"io/fs"
	"strconv"
	"syscall"
)

func agentsFileIdentity(i fs.FileInfo) string {
	if value, ok := i.(agentsIdentityInfo); ok {
		return value.identity
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok || s.Dev == 0 && s.Ino == 0 {
		return ""
	}
	return strconv.FormatUint(uint64(s.Dev), 10) + ":" + strconv.FormatUint(uint64(s.Ino), 10)
}

func withAgentsNativeIdentity(_ string, info fs.FileInfo, _ bool) fs.FileInfo { return info }
