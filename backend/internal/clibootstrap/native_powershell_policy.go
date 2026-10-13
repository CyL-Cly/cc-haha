package clibootstrap

import (
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

func (m *MainPrint) nativePowerShellSandboxPolicy(scope *cooperative.Scope) error {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if !windowshell.IsWindows(m.options.Platform) {
		return nil
	}
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return err
	}
	sandbox, _ := settings.Object("sandbox")
	if string(sandbox.Get("enabled")) == "true" && string(sandbox.Get("allowUnsandboxedCommands")) == "false" {
		return errors.New(builtintools.WindowsPowerShellSandboxRefusal)
	}
	return nil
}
