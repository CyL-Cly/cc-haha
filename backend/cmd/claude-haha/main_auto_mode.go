package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func runAutoModeCommand(ctx context.Context, args []string, parsed *cliargs.Parsed) int {
	native, err := newMainNativeBoundary()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return runNativeAutoModeCommand(ctx, args, parsed, native, os.Stdout, os.Stderr)
}

func runNativeAutoModeCommand(ctx context.Context, args []string, parsed *cliargs.Parsed, native *clibootstrap.NativeBoundary, out, stderr io.Writer) int {
	m, err := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Context: ctx, Arguments: args, Native: native, Platform: runtime.GOOS, Version: version, PID: os.Getpid(), NonInteractive: true, Output: out, Error: stderr})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer m.Close()
	if err := m.BeginNativeAPIPreconnect(ctx); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	model, _ := parsed.Options["model"].(string)
	code := 1
	err = m.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		code, err = m.AutoMode(ctx, scope, strings.TrimPrefix(parsed.Path, "claude auto-mode "), model)
		return err
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return code
}
