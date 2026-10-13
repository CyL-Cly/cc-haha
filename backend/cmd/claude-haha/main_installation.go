package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func runInstallationCommand(ctx context.Context, args []string, parsed *cliargs.Parsed) int {
	native, err := newMainNativeBoundary()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return runNativeInstallationCommand(ctx, args, parsed, native, os.Stdout, os.Stderr)
}

func runNativeInstallationCommand(ctx context.Context, args []string, parsed *cliargs.Parsed, native *clibootstrap.NativeBoundary, out, diagnostics io.Writer) int {
	m, err := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Arguments: args, Native: native, Platform: runtime.GOOS, Version: version, PID: os.Getpid(), NonInteractive: true, Output: out, Error: diagnostics})
	if err != nil {
		fmt.Fprintln(diagnostics, err)
		return 1
	}
	defer m.Close()
	if parsed.Path == "claude doctor" {
		if err := runNativeDoctorCommand(ctx, m, out); err != nil {
			fmt.Fprintln(diagnostics, err)
			return 1
		}
		return 0
	}
	code := 0
	err = m.Run(ctx, func(scope *cooperative.Scope) error {
		target := ""
		if len(parsed.Args) > 0 {
			target = parsed.Args[0]
		}
		force := parsed.Options["force"] == true
		if parsed.Path == "claude update" {
			var err error
			target, err = m.InstallationChannel(scope)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Current version: %s\nChecking for updates to %s version...\n", version, target)
		} else {
			fmt.Fprintln(out, "Installing Claude Code Go native build...")
		}
		result, err := m.Installation(ctx, scope, target, force, nil)
		if err != nil {
			return err
		}
		if result.LockFailed {
			if parsed.Path == "claude update" {
				fmt.Fprintln(out, "Another Claude process is currently running. Please try again in a moment.")
				return nil
			}
			code = 1
			fmt.Fprintln(diagnostics, "Could not install - another process is currently installing Claude. Please try again in a moment.")
			return nil
		}
		if parsed.Path == "claude install" {
			cleanup, err := m.CleanupInstallationLegacy(ctx, scope)
			if err != nil {
				return err
			}
			if err := m.SaveInstallationChannel(scope, target); err != nil {
				return err
			}
			fmt.Fprintf(out, "Claude Code installation completed successfully\nVersion: %s\nLocation: %s\n", result.Version, result.Executable)
			for _, warning := range cleanup.Warnings {
				fmt.Fprintln(out, warning)
			}
			for _, alias := range cleanup.Aliases {
				fmt.Fprintln(out, alias.Message)
			}
		} else if result.Version == version {
			fmt.Fprintf(out, "Claude Code is up to date (%s)\n", version)
		} else {
			fmt.Fprintf(out, "Successfully updated from %s to version %s\n", version, result.Version)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(diagnostics, err)
		return 1
	}
	return code
}
