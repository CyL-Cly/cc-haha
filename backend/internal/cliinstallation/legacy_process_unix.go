//go:build !windows

package cliinstallation

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

func legacyProcess(ctx context.Context, name string, args []string, env map[string]string) *exec.Cmd {
	path := windowshell.FindNativeExecutable(runtime.GOOS, name, env)
	if path == "" {
		return nil
	}
	command := exec.CommandContext(ctx, path, args...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = 5 * time.Second
	return command
}

func unlinkLegacy(path string) error { return syscall.Unlink(path) }
