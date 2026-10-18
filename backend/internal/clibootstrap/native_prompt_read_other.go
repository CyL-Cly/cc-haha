//go:build !darwin

package clibootstrap

import (
	"context"
	"os"
)

// Keep the existing physical read on other hosts. In-flight descriptor
// cancellation there needs its own actual host consumer evidence.
func nativePromptReadFile(ctx context.Context, path string) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	return os.ReadFile(path)
}
