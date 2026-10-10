//go:build !windows

package builtintools

import (
	"os"
	"syscall"
)

func openTaskOutput(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW, 0666)
}
