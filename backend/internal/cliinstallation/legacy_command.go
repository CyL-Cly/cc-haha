package cliinstallation

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// runLegacyCommand is the native execFileNoThrowWithCwd boundary. In particular,
// a missing npm executable is an unsuccessful uninstall, not an install failure.
func runLegacyCommand(ctx context.Context, name string, args []string, cwd, home string, env map[string]string) (CommandResult, error) {
	worker, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	command := legacyProcess(worker, name, args, env)
	if command == nil {
		return CommandResult{Code: 1}, nil
	}
	command.Dir = cwd
	if command.Dir == "" {
		command.Dir = home
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	command.Env = []string{}
	for _, key := range keys {
		command.Env = append(command.Env, key+"="+env[key])
	}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	result := CommandResult{Stdout: stripFinalNewline(string(protocol.DecodeJavaScriptUTF8(stdout.Bytes()))), Stderr: stripFinalNewline(string(protocol.DecodeJavaScriptUTF8(stderr.Bytes())))}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			result.Code = exit.ExitCode()
			if result.Code < 0 {
				result.Code = 1
			}
		} else {
			return CommandResult{Code: 1}, nil
		}
	}
	return result, nil
}

func stripFinalNewline(text string) string {
	if strings.HasSuffix(text, "\n") {
		return strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")
	}
	return text
}
