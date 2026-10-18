//go:build windows

package tui

import (
	"context"
	"os"
	"os/exec"

	"github.com/NanmiCoder/cc-haha/backend/internal/processpolicy"
)

func configureFileEditorProcess(command *exec.Cmd) func() error {
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if processpolicy.KillWindowsProcessTree(context.Background(), command.Process.Pid, processpolicy.WindowsTreeKillOptions{}) {
			return nil
		}
		return command.Process.Kill()
	}
	return func() error { return nil }
}
