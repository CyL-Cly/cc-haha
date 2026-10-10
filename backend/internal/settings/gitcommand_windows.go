//go:build windows

package settings

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func gitCommand(ctx context.Context, executable string, args []string, environment map[string]string) *exec.Cmd {
	extension := strings.ToLower(filepath.Ext(executable))
	if extension == ".exe" || extension == ".com" {
		return exec.CommandContext(ctx, executable, args...)
	}
	shell := environmentValue(environment, "COMSPEC")
	if shell == "" {
		if root := environmentValue(environment, "SYSTEMROOT"); root != "" {
			shell = filepath.Join(root, "System32", "cmd.exe")
		}
	}
	if shell == "" {
		return nil
	}
	command := exec.CommandContext(ctx, shell)
	// Go's normal argv quoting targets CommandLineToArgvW; cmd.exe has different
	// rules. Pass the completed command line verbatim, as cross-spawn does.
	command.SysProcAttr = &syscall.SysProcAttr{CmdLine: windowsBatchCommandLine(shell, filepath.Clean(executable), args)}
	return command
}
