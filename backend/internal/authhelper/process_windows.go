//go:build windows

package authhelper

import (
	"context"
	"errors"
	"os/exec"
)

func shellCommand(context.Context, string) (*exec.Cmd, error) {
	return nil, errors.New("apiKeyHelper capability is not migrated: Windows execa shell semantics")
}
