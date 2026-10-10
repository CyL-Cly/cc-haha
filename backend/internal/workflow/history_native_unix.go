//go:build !windows

package workflow

import (
	"os"
	"syscall"
)

func openTaskOutput(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
