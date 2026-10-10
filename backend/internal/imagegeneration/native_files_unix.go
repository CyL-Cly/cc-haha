//go:build !windows

package imagegeneration

import (
	"io"
	"os"
	"syscall"
)

func openImageNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
func readImageFile(file *os.File) ([]byte, error) { return io.ReadAll(file) }
