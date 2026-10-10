//go:build windows

package builtintools

import "os/exec"

// Windows runtime admission remains closed until native job/ConPTY ownership
// replaces this platform-neutral compile seam.
func configureProcess(cmd *exec.Cmd) {}
