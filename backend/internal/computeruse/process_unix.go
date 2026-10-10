//go:build !windows

package computeruse

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureNativeProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

func configureWindowsBadge(command *exec.Cmd) {}

func stopWindowsBadge(command *exec.Cmd) error { return command.Process.Signal(syscall.SIGTERM) }
