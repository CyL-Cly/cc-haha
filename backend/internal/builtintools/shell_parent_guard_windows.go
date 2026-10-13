//go:build windows

package builtintools

import "os/exec"

// MAIN has no POSIX lifetime guardian on Windows. Graceful cancellation still
// terminates the anchored descendant tree via absolute System32/taskkill.exe.
func configureShellProcess(cmd *exec.Cmd) { configureProcess(cmd) }
