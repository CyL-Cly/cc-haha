//go:build !windows

package queryhooks

import (
	"os"
	"syscall"
)

func openHookOutput(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0666)
}
