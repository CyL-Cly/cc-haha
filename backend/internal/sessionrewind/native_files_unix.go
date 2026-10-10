//go:build !windows

package sessionrewind

import (
	"os"
	"syscall"
)

func openNoFollow(path string, flags int, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flags|syscall.O_NOFOLLOW, mode)
}
func fileLinkCount(_ *os.File, info os.FileInfo) (uint64, error) {
	return uint64(info.Sys().(*syscall.Stat_t).Nlink), nil
}
func pathLinkCount(_ string, info os.FileInfo) (uint64, error) {
	return uint64(info.Sys().(*syscall.Stat_t).Nlink), nil
}

func writableAccess(path string) error { return syscall.Access(path, 2) }
