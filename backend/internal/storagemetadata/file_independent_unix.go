//go:build !windows

package storagemetadata

import (
	"os"
	"syscall"
)

func independentFile(_ string, info os.FileInfo) (bool, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1, nil
}
