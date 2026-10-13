//go:build !windows

package cliinstallation

import "syscall"

func processRunning(pid int) bool { return pid > 1 && syscall.Kill(pid, 0) == nil }
