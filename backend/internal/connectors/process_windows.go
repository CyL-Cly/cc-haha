//go:build windows

package connectors

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/processpolicy"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if processpolicy.KillWindowsProcessTree(context.Background(), cmd.Process.Pid, processpolicy.WindowsTreeKillOptions{}) {
			return nil
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 2 * time.Second
}
