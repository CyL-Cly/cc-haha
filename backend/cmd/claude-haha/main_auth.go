package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func runAuthCommand(ctx context.Context, args []string, parsed *cliargs.Parsed) int {
	native, err := newMainNativeBoundary()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	stat, e := os.Stdout.Stat()
	nonInteractive := e != nil || stat.Mode()&os.ModeCharDevice == 0
	return runNativeAuthCommand(ctx, args, parsed, native, nonInteractive, os.Stdout, os.Stderr)
}

func runNativeAuthCommand(ctx context.Context, args []string, parsed *cliargs.Parsed, native *clibootstrap.NativeBoundary, nonInteractive bool, out, stderr io.Writer) int {
	m, err := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Input: os.Stdin, Context: ctx, Arguments: args, Native: native, Platform: runtime.GOOS, Version: version, PID: os.Getpid(), NonInteractive: nonInteractive, Output: out, Error: stderr})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer m.Close()
	if err := m.BeginNativeAPIPreconnect(ctx); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := cliauth.Result{ExitCode: 1}
	err = m.Run(ctx, func(scope *cooperative.Scope) error {
		switch parsed.Path {
		case "claude auth status":
			var e error
			result, e = m.AuthStatus(ctx, scope, parsed.Options["openai"] == true, parsed.Options["text"] == true)
			return e
		case "claude auth login":
			if parsed.Options["openai"] == true && (parsed.Options["email"] != nil && parsed.Options["email"] != "" || parsed.Options["sso"] == true || parsed.Options["console"] == true || parsed.Options["claudeai"] == true) {
				result.Stderr = "Error: --openai cannot be combined with --email, --sso, --console, or --claudeai.\n"
				return nil
			}
			if parsed.Options["console"] == true && parsed.Options["claudeai"] == true {
				result.Stderr = "Error: --console and --claudeai cannot be used together.\n"
				return nil
			}
			var e error
			options := cliAuthLoginOptions(native)
			options.Version = version
			options.Input = cliauth.LoginInput{OpenAI: parsed.Options["openai"] == true, SSO: parsed.Options["sso"] == true, Console: parsed.Options["console"] == true, ClaudeAI: parsed.Options["claudeai"] == true}
			if email, ok := parsed.Options["email"].(string); ok {
				options.Input.Email = email
			}
			result, e = m.AuthLogin(ctx, scope, options)
			return e
		case "claude auth logout":
			var e error
			result, e = m.AuthLogout(ctx, scope, parsed.Options["openai"] == true)
			return e
		default:
			return &clibootstrap.MainCapabilityError{Boundary: "auth parsed action"}
		}
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if result.Diagnostic != nil {
		fmt.Fprintln(stderr, result.Diagnostic.Message)
		return 1
	}
	if _, err = io.WriteString(out, result.Stdout); err == nil {
		_, err = io.WriteString(stderr, result.Stderr)
	}
	if err != nil {
		return 1
	}
	return result.ExitCode
}
