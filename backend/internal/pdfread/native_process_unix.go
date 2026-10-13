//go:build !windows

package pdfread

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func isWindowsPDFProcess() bool { return false }
func configurePDFProcess(command *exec.Cmd, _ map[string]string) {
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
