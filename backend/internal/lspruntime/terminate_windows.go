//go:build windows

package lspruntime

import "os"

// Node child_process.kill's default SIGTERM maps to TerminateProcess on Windows.
func terminateProcess(process *os.Process) error { return process.Kill() }
