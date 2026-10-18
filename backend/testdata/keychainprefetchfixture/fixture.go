// Package keychainprefetchfixture substitutes only the executable primitive.
// The Store and MainCommand still launch, wait, parse and publish actual reads.
package keychainprefetchfixture

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

// Bind accepts fixture data from an already substituted storage primitive.
// It never resolves or invokes security(1), even if the callback is absent.
func Bind(temporary, directory, home string, read func(context.Context, securestorage.Command) ([]byte, error)) func(context.Context, securestorage.Command) (*securestorage.KeychainPrefetchProcess, error) {
	return func(ctx context.Context, call securestorage.Command) (*securestorage.KeychainPrefetchProcess, error) {
		if ctx == nil || read == nil || !filepath.IsAbs(temporary) || !filepath.IsAbs(directory) || !filepath.IsAbs(home) || len(call.Args) != 6 || call.Args[0] != "find-generic-password" || call.ShellScript != "" {
			return nil, errors.New("unbound private Keychain prefetch operation")
		}
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		data, failure := read(ctx, call)
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		root, err := os.MkdirTemp(temporary, "owned-prefetch-")
		if err != nil {
			return nil, err
		}
		var command *exec.Cmd
		processDirectory := directory
		if runtime.GOOS == "windows" {
			systemRoot := os.Getenv("SystemRoot")
			if !filepath.IsAbs(systemRoot) {
				return nil, errors.New("absolute Windows system runtime required")
			}
			operation := "exit 44"
			if failure == nil {
				if err := os.WriteFile(filepath.Join(root, "payload.bin"), data, 0600); err != nil {
					return nil, err
				}
				operation = "type payload.bin"
			}
			// The command string contains no caller path or fixture bytes.
			// cmd's built-in type reads only this owned directory's payload.
			command = exec.CommandContext(ctx, filepath.Join(systemRoot, "System32", "cmd.exe"), "/d", "/s", "/c", operation)
			processDirectory = root
		} else {
			program := filepath.Join(root, "security.sh")
			body := "#!/bin/sh\nexit 44\n"
			if failure == nil {
				body = "#!/bin/sh\nexec /bin/cat\n"
			}
			if err := os.WriteFile(program, []byte(body), 0700); err != nil {
				return nil, err
			}
			command = exec.CommandContext(ctx, program)
			if failure == nil {
				command.Stdin = bytes.NewReader(data)
			}
		}
		command.Dir = processDirectory
		command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + temporary, "TMP=" + temporary, "TEMP=" + temporary, "PATH=/usr/bin:/bin"}
		return securestorage.StartKeychainPrefetchProcess(ctx, command)
	}
}
