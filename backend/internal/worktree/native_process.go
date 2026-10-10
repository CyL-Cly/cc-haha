package worktree

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NativeRun uses the supplied environment for both executable resolution and
// child launch. Tests supply a disposable HOME and a local-only git remote.
func NativeRun(ctx context.Context, r ProcessRequest) (ProcessResult, error) {
	executable := r.Command
	if !filepath.IsAbs(executable) {
		executable = ""
		for _, dir := range filepath.SplitList(r.Environment["PATH"]) {
			path := filepath.Join(dir, r.Command)
			if info, e := os.Stat(path); e == nil && !info.IsDir() && info.Mode()&0111 != 0 {
				executable = path
				break
			}
		}
		if executable == "" {
			return ProcessResult{Code: 1, Stderr: r.Command + ": command not found"}, nil
		}
	}
	command := exec.CommandContext(ctx, executable, r.Arguments...)
	command.Dir = r.CWD
	keys := []string{}
	for key := range r.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	command.Env = []string{}
	for _, key := range keys {
		command.Env = append(command.Env, key+"="+r.Environment[key])
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	e := command.Run()
	result := ProcessResult{Stdout: string(protocol.DecodeJavaScriptUTF8(stdout.Bytes())), Stderr: string(protocol.DecodeJavaScriptUTF8(stderr.Bytes()))}
	if e != nil {
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			result.Code = exit.ExitCode()
		} else {
			result.Code = 1
			if result.Stderr == "" {
				result.Stderr = e.Error()
			}
		}
	}
	return result, nil
}
func NativeGitExecutable(env map[string]string) string {
	for _, dir := range filepath.SplitList(env["PATH"]) {
		path := filepath.Join(dir, "git")
		if info, e := os.Stat(path); e == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return path
		}
	}
	return "git"
}
