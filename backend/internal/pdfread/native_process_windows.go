//go:build windows

package pdfread

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/processpolicy"
	"os"
	"os/exec"
	"time"
)

func isWindowsPDFProcess() bool { return true }
func configurePDFProcess(command *exec.Cmd, environment map[string]string) {
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if processpolicy.KillWindowsProcessTree(ctx, command.Process.Pid, processpolicy.WindowsTreeKillOptions{Environment: command.Env}) {
			return nil
		}
		return command.Process.Kill()
	}
}
