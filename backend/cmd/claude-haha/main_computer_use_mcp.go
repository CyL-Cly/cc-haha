package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/NanmiCoder/cc-haha/backend/internal/computeruse"
	"github.com/NanmiCoder/cc-haha/backend/internal/computerusemcp"
)

func runComputerUseMCP(ctx context.Context) int {
	environment := processEnvironment()
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	home := environment["HOME"]
	if runtime.GOOS == "windows" && environment["USERPROFILE"] != "" {
		home = environment["USERPROFILE"]
	}
	config := environment["CLAUDE_CONFIG_DIR"]
	if config == "" {
		config = filepath.Join(home, ".claude")
	}
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	service, err := computeruse.New(computeruse.Options{HomeDir: home, ConfigDir: config, WorkDir: cwd, TempDir: os.TempDir(), Executable: executable, Environment: environment, Platform: runtime.GOOS})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	stop := context.AfterFunc(ctx, func() { _ = os.Stdin.Close() })
	defer stop()
	if err = computerusemcp.RunStdio(ctx, computerusemcp.StdioOptions{Service: service, Platform: runtime.GOOS, Input: os.Stdin, Output: os.Stdout}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
