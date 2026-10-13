//go:build !windows

package processpolicy

import (
	"context"
	"errors"
)

func runWindowsTreeKiller(context.Context, string, []string, []string) error {
	return errors.New("Windows process-tree termination requires Windows")
}
