package main

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	"os"
	"runtime"
)

func runTmuxWorktreeFastPath(ctx context.Context, args []string) (int, bool) {
	if !worktree.HasTmuxWorktreeFastPath(args) {
		return 0, false
	}
	cwd, e := os.Getwd()
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1, true
	}
	// The Source fast path precedes provider/auth and all MainPrint imports.
	native, e := newNativeBoundaryForEnvironment(tmuxEntrypointEnvironment(processEnvironment()), cwd, runtime.GOOS)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1, true
	}
	executable, e := os.Executable()
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1, true
	}
	result, e := clibootstrap.RunNativeTmuxWorktree(ctx, args, native, runtime.GOOS, executable, clibootstrap.TmuxFastPathIO{Input: os.Stdin, Output: os.Stdout, Error: os.Stderr})
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1, true
	}
	if result.Handled {
		return 0, true
	}
	if result.Error != "" {
		fmt.Fprintln(os.Stderr, result.Error)
		return 1, true
	}
	return 0, false
}

// These two original cli.tsx top-level assignments precede its fast paths.
// SIMPLE/entrypoint/full CLI initialization still runs only in the inner CLI.
func tmuxEntrypointEnvironment(input map[string]string) map[string]string {
	env := map[string]string{}
	for k, v := range input {
		env[k] = v
	}
	env["COREPACK_ENABLE_AUTO_PIN"] = "0"
	if env["CLAUDE_CODE_REMOTE"] == "true" {
		existing := env["NODE_OPTIONS"]
		if existing != "" {
			env["NODE_OPTIONS"] = existing + " --max-old-space-size=8192"
		} else {
			env["NODE_OPTIONS"] = "--max-old-space-size=8192"
		}
	}
	return env
}
