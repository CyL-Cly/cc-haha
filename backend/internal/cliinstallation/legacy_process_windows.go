//go:build windows

package cliinstallation

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/processpolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

func legacyProcess(ctx context.Context, name string, args []string, env map[string]string) *exec.Cmd {
	path := windowshell.FindNativeExecutable("windows", name, env)
	if path == "" {
		return nil
	}
	invocation := windowshell.ProjectWindowsInvocation(name, path, args, env)
	executable := path
	if invocation.Verbatim {
		executable = windowshell.FindNativeExecutable("windows", invocation.Command, env)
		if executable == "" {
			executable = invocation.Command
		}
	}
	command := exec.CommandContext(ctx, executable, invocation.Arguments...)
	command.Args[0] = invocation.Command
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if invocation.Verbatim {
		command.SysProcAttr.CmdLine = syscall.EscapeArg(invocation.Command) + " " + strings.Join(invocation.Arguments, " ")
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
	command.WaitDelay = 5 * time.Second
	return command
}

func unlinkLegacy(path string) error {
	value, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return syscall.DeleteFile(value)
}
