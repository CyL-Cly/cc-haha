package main

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"io"
	"os"
	"runtime"
	"strings"
)

func runPluginCommand(ctx context.Context, args []string, parsed *cliargs.Parsed) int {
	native, err := newMainNativeBoundary()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return runNativePluginCommand(ctx, args, parsed, native, os.Stdout, os.Stderr)
}
func runNativePluginCommand(ctx context.Context, args []string, parsed *cliargs.Parsed, native *clibootstrap.NativeBoundary, out, diagnostics io.Writer) int {
	command, err := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Arguments: args, Native: native, Platform: runtime.GOOS, PID: os.Getpid(), Version: version, NonInteractive: true, Output: out, Error: diagnostics})
	if err != nil {
		fmt.Fprintln(diagnostics, err)
		return 1
	}
	defer command.Close()
	action := strings.TrimPrefix(parsed.Path, "claude plugin ")
	action = strings.ReplaceAll(action, " ", "-")
	input := plugincli.Command{Action: action, JSON: parsed.Options["json"] == true, Available: parsed.Options["available"] == true, Cowork: parsed.Options["cowork"] == true, All: parsed.Options["all"] == true, KeepData: parsed.Options["keepData"] == true}
	if value, ok := parsed.Options["scope"].(string); ok {
		input.Scope = value
	}
	if values, ok := parsed.Options["sparse"].([]any); ok {
		for _, value := range values {
			if path, ok := value.(string); ok {
				input.Sparse = append(input.Sparse, path)
			}
		}
	}
	for _, value := range parsed.ProcessedArgs {
		if s, ok := value.(string); ok {
			input.Argument = s
			break
		}
	}
	var result plugincli.Result
	err = command.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		result, err = command.PluginCommand(ctx, scope, input)
		return err
	})
	if err != nil {
		fmt.Fprintln(diagnostics, err)
		return 1
	}
	_, _ = io.WriteString(out, result.Stdout[result.StreamedBytes:])
	_, _ = io.WriteString(diagnostics, result.Stderr)
	return result.ExitCode
}
