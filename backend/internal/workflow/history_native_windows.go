//go:build windows

package workflow

import "os"

// The original Windows O_NOFOLLOW constant is absent; lstat and descriptor
// checks remain active in the caller, as in the Source implementation.
func openTaskOutput(path string) (*os.File, error) { return os.Open(path) }
