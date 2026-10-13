//go:build windows

package authhelper

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
	"os/exec"
)

func shellCommand(ctx context.Context, command string, options ProcessOptions) (*exec.Cmd, error) {
	return windowshell.NewWindowsShellCommand(ctx, command, options.Environment), nil
}
