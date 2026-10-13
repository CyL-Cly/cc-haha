//go:build windows

package mcpheaders

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
	"os/exec"
)

func nativeCommand(ctx context.Context, command string, env map[string]string) *exec.Cmd {
	return windowshell.NewWindowsShellCommand(ctx, command, env)
}
