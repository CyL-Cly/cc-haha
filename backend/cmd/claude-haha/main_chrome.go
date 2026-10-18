package main

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/NanmiCoder/cc-haha/backend/internal/chromenative"
)

func runChromeFastPath(ctx context.Context, args []string) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	switch args[0] {
	case "--claude-in-chrome-mcp":
		// The frozen external Source aliases the browser MCP package to an
		// empty connect() stub. No browser tools or initialize response are
		// shipped in this profile; the entry point settles without a server.
		return 0, true
	case "--chrome-native-host":
		env := processEnvironment()
		home := env["HOME"]
		if runtime.GOOS == "windows" && env["USERPROFILE"] != "" {
			home = env["USERPROFILE"]
		}
		if err := chromenative.Run(ctx, chromenative.Options{Input: os.Stdin, Output: os.Stdout, Error: os.Stderr, Environment: env, Home: home}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1, true
		}
		return 0, true
	}
	return 0, false
}
