//go:build !windows

package clibootstrap

import (
	"os"
	"syscall"
)

func openNativeRemoteAgentOutput(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW, 0666)
}
