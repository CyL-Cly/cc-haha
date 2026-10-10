//go:build windows

package computeruse

import (
	"os/exec"
	"syscall"
)

func configureNativeProcess(command *exec.Cmd) {}

func configureWindowsBadge(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func stopWindowsBadge(command *exec.Cmd) error { return command.Process.Kill() }
