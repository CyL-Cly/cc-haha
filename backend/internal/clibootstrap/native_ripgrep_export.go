package clibootstrap

import (
	"os"
	"path/filepath"
)

// NativeRipgrepCommand reuses the original resolver already used by native
// Agent discovery. Search does not create a second executable selection policy.
func NativeRipgrepCommand(n *NativeBoundary) (string, []string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	command, err := nativeAgentRipgrepConfig(n, executable, filepath.Join(filepath.Dir(executable), "vendor", "ripgrep"))
	return command.Command, append([]string{}, command.Args...), err
}
