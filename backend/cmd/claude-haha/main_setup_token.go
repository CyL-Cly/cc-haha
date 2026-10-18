package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func runSetupTokenCommand(ctx context.Context, args []string) int {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	signalExit := func() int {
		select {
		case received := <-signals:
			if received == syscall.SIGTERM {
				return 143
			}
			return 0
		default:
			return 130
		}
	}
	native, e := newMainNativeBoundary()
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	stat, e := os.Stdout.Stat()
	nonInteractive := e != nil || stat.Mode()&os.ModeCharDevice == 0
	m, e := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Input: os.Stdin, Context: ctx, Arguments: args, Native: native, Platform: runtime.GOOS, Version: version, PID: os.Getpid(), NonInteractive: nonInteractive, Output: os.Stdout, Error: os.Stderr})
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	defer m.Close()
	if e := m.BeginNativeAPIPreconnect(ctx); e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	external := cliAuthLoginOptions(native)
	var result cliauth.Result
	e = m.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		result, e = m.SetupToken(ctx, scope, clibootstrap.SetupTokenOptions{HTTP: external.HTTP, Config: external.Config, Browser: external.Browser, Input: os.Stdin, TerminalEnvironment: os.Environ()})
		return e
	})
	if e != nil {
		if ctx.Err() != nil {
			return signalExit()
		}
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	if ctx.Err() != nil {
		return signalExit()
	}
	return result.ExitCode
}
