//go:build !windows

package windowshell

import (
	"os/exec"
	"time"
)

func configureProbeProcess(command *exec.Cmd) { command.WaitDelay = 2 * time.Second }
