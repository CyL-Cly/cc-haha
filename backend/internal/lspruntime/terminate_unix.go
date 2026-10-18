//go:build !windows

package lspruntime

import (
	"os"
	"syscall"
)

func terminateProcess(process *os.Process) error { return process.Signal(syscall.SIGTERM) }
