//go:build !windows

package ideintegration

import (
	"os"
	"os/exec"
	"syscall"
)

func nativeProcessRunning(pid int) bool {
	process, e := os.FindProcess(pid)
	return e == nil && process.Signal(syscall.Signal(0)) == nil
}
func nativeExecutableMode(mode os.FileMode) bool { return mode&0111 != 0 }
func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process != nil {
			return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
}
