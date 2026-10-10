//go:build !windows

package settings

import (
	"context"
	"os/exec"
)

func gitCommand(ctx context.Context, executable string, args []string, _ map[string]string) *exec.Cmd {
	return exec.CommandContext(ctx, executable, args...)
}
