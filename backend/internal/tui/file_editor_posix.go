//go:build !windows

package tui

import (
	"os"
	"os/exec"
	"sync"
)

func configureFileEditorProcess(command *exec.Cmd) func() error {
	// Keep the controlling terminal/group. Only the spawned editor tree is owned.
	var mu sync.Mutex
	var cancellationError error
	command.Cancel = func() error {
		err := cancelFileEditorProcess(command.Process)
		if err != nil && err != os.ErrProcessDone {
			mu.Lock()
			cancellationError = err
			mu.Unlock()
		}
		return err
	}
	return func() error { mu.Lock(); defer mu.Unlock(); return cancellationError }
}
