package securestorage

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func newSecurityCommand(ctx context.Context, request Command, directory string, environment map[string]string) *exec.Cmd {
	// Resolve against the supplied current environment. exec.Command's own
	// LookPath would use the hosting Go process's unrelated ambient PATH.
	command := exec.CommandContext(ctx, filepath.Join(directory, "security"), request.Args...)
	path, present := environment["PATH"]
	if !present {
		path = "/usr/bin:/bin"
	}
	executable := ""
	for _, part := range filepath.SplitList(path) {
		// Pinned Bun's direct spawn skips empty PATH fields. The synchronous
		// shell remains responsible for its own empty-field cwd semantics.
		if part == "" {
			continue
		}
		candidate := filepath.Join(part, "security")
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(directory, candidate)
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode().Perm()&0111 != 0 {
			executable = candidate
			break
		}
	}
	if executable == "" {
		command.Path, command.Args[0] = "security", "security"
		command.Err = &exec.Error{Name: "security", Err: exec.ErrNotFound}
	} else {
		command.Path, command.Args[0] = executable, executable
	}
	if request.ShellScript != "" {
		command = exec.CommandContext(ctx, "/bin/sh", "-c", request.ShellScript)
	}
	command.Env = []string{}
	for name, value := range environment {
		command.Env = append(command.Env, name+"="+value)
	}
	command.Dir = directory
	if request.Input != "" {
		command.Stdin = strings.NewReader(request.Input)
	}
	return command
}

func nativeProcessReceipt(err error) bool {
	var exit *exec.ExitError
	var path *os.PathError
	var lookup *exec.Error
	return err == nil || errors.As(err, &exit) || errors.As(err, &path) || errors.As(err, &lookup)
}
