//go:build !windows

package mcpclient

import (
	"os/exec"
	"strings"
	"syscall"
)

func configureMCPProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func prepareMCPCommand(cmd *exec.Cmd, env map[string]string, cwd string) error {
	if strings.ContainsAny(cmd.Args[0], "/\\") {
		return nil
	}
	resolved, err := lookupCommand(cmd.Args[0], env["PATH"], cwd)
	if err != nil {
		return err
	}
	cmd.Path = resolved
	// exec.Command performs its lookup before Env is assigned.
	cmd.Err = nil
	return nil
}
func signalMCPProcess(cmd *exec.Cmd, level int) error {
	signal := syscall.SIGINT
	if level == 1 {
		signal = syscall.SIGTERM
	}
	if level == 2 {
		signal = syscall.SIGKILL
	}
	// Keep the whole owned child tree bound to the same lifetime. Shell and
	// server grandchildren must not retain protocol pipes after cancellation.
	return syscall.Kill(-cmd.Process.Pid, signal)
}
