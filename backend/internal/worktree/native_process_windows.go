//go:build windows

package worktree

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/processpolicy"
)

func configureNativeWorktreeProcess(command *exec.Cmd, verbatim bool) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if verbatim {
		command.SysProcAttr.CmdLine = syscall.EscapeArg(command.Args[0]) + " " + strings.Join(command.Args[1:], " ")
	}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if processpolicy.KillWindowsProcessTree(context.Background(), command.Process.Pid, processpolicy.WindowsTreeKillOptions{Environment: command.Env}) {
			return nil
		}
		return command.Process.Kill()
	}
	command.WaitDelay = 2 * time.Second
}
