//go:build !windows

package opentargets

import (
	"os/exec"
	"syscall"
)

func configureLaunch(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func configureProbe(cmd *exec.Cmd)  {}
