package settings

import (
	"context"
	"os/exec"
)

// NativeProcessCommand reuses the existing Source execa/cross-spawn executable
// and Windows batch quoting boundary. It supplies no settings or environment;
// the caller still owns the original launch snapshot and process lifecycle.
func NativeProcessCommand(ctx context.Context, executable string, args []string, environment map[string]string) *exec.Cmd {
	return gitCommand(ctx, executable, args, environment)
}
