//go:build windows

package windowshell

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/processpolicy"
)

func configureProbeProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if processpolicy.KillWindowsProcessTree(context.Background(), command.Process.Pid, processpolicy.WindowsTreeKillOptions{}) {
			return nil
		}
		return command.Process.Kill()
	}
	command.WaitDelay = 2 * time.Second
}
