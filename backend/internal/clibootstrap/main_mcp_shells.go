package clibootstrap

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

func (m *NativeMCPToolContext) nativeMCPShellOptions(options builtintools.Options) (builtintools.Options, error) {
	if !windowshell.IsWindows(m.platform) {
		return options, nil
	}
	n := m.command.Native
	resolver := windowshell.NewNative(m.platform, n.CWD, n.Environment)
	err := m.command.Run(context.Background(), func(scope *cooperative.Scope) error {
		future, err := nativeOwnedWork(m.command.Owner, m.binding, scope, func() (windowshell.Snapshot, error) { return resolver.Warm(context.Background()), nil })
		if err != nil {
			return err
		}
		view, err := future.Await(scope)
		if err != nil {
			return err
		}
		if view.GitBash != "" {
			n.Environment["SHELL"] = view.GitBash
			if err := m.command.Settings.environment.SetProperty(quoteMain("SHELL"), quoteMain(view.GitBash)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return options, err
	}
	options.Shells = resolver
	options.BashCwd = &builtintools.BashCwdBindings{Owner: m.command.Owner, TemporaryDirectory: n.Temp, Changed: func(scope *cooperative.Scope, _, cwd string) error {
		if scope == nil || !scope.BelongsTo(m.command.Owner) || m.tools == nil {
			return cooperative.ErrRestricted
		}
		info, err := n.stat(cwd)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("PowerShell working directory is not a directory")
		}
		return m.tools.SetWorkingDirectory(scope, cwd)
	}}
	options.Images = &builtintools.ImageBindings{NormalizeShellData: m.nativeMCPNormalizeShellImage, ReadPDF: m.startNativeMCPPDFRead}
	options.WindowsPowerShellSandboxPolicy = func(scope *cooperative.Scope) error {
		if scope == nil || !scope.BelongsTo(m.command.Owner) {
			return cooperative.ErrRestricted
		}
		settings, err := m.command.Startup.GetInitialSettings()
		if err != nil || settings == nil {
			return err
		}
		sandbox, _ := settings.Object("sandbox")
		if string(sandbox.Get("enabled")) == "true" && string(sandbox.Get("allowUnsandboxedCommands")) == "false" {
			return errors.New(builtintools.WindowsPowerShellSandboxRefusal)
		}
		return nil
	}
	return options, nil
}
