//go:build windows

package windowshell

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/processpolicy"
)

func NewWindowsShellCommand(ctx context.Context, command string, environment map[string]string) *exec.Cmd {
	invocation := ProjectWindowsShellInvocation(command, environment)
	executable := FindNativeExecutable("windows", invocation.Command, environment)
	if executable == "" {
		executable = invocation.Command
	}
	cmd := exec.CommandContext(ctx, executable, invocation.Arguments...)
	cmd.Args[0] = invocation.Command
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if invocation.Verbatim {
		cmd.SysProcAttr.CmdLine = syscall.EscapeArg(invocation.Command) + " " + strings.Join(invocation.Arguments, " ")
	}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if processpolicy.KillWindowsProcessTree(context.Background(), cmd.Process.Pid, processpolicy.WindowsTreeKillOptions{Environment: cmd.Env}) {
			return nil
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 2 * time.Second
	return cmd
}
