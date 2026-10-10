//go:build !windows

package agentcatalog

import (
	"fmt"
	"io/fs"
	"syscall"
)

func fixtureIdentity(i fs.FileInfo) string {
	v, ok := i.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d:%d", v.Dev, v.Ino)
}
