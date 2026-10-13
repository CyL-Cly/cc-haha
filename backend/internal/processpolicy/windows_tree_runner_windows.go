//go:build windows

package processpolicy

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

func runWindowsTreeKiller(ctx context.Context, executable string, args, environment []string) error {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = environment
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.WaitDelay = 2 * time.Second
	return cmd.Run()
}
