//go:build !windows

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestMCPCLIActualCallerCancelsAndJoinsBothPhysicalStartupSecurityChildren(t *testing.T) {
	n, root := mcpCLIPrivateNative(t)
	n.Environment["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] = "1"
	n.Environment["CLAUDE_CODE_DISABLE_ADVISOR_TOOL"] = "1"
	n.Environment["NODE_ENV"] = "production"
	program := filepath.Join(root, "owned-held-security.sh")
	// Replace the shell: cancellation must reap the actual held process,
	// without a descendant keeping stdout/stderr or the test alive.
	if err := os.WriteFile(program, []byte("#!/bin/sh\nexec /usr/bin/tail -f /dev/null\n"), 0700); err != nil {
		t.Fatal(err)
	}
	started := make(chan *securestorage.KeychainPrefetchProcess, 2)
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("unexpected sync security in disabled advisor profile")
	}
	n.StartKeychainPrefetch = func(ctx context.Context, call securestorage.Command) (*securestorage.KeychainPrefetchProcess, error) {
		if call.ShellScript != "" || len(call.Args) != 6 || call.Args[0] != "find-generic-password" || !strings.HasPrefix(call.Args[5], "Claude Code") {
			return nil, errors.New("unexpected prefetch primitive")
		}
		command := exec.CommandContext(ctx, program)
		command.Dir, command.Env = n.CWD, []string{"HOME=" + n.Home, "TMPDIR=" + n.Temp, "PATH=/usr/bin:/bin"}
		process, err := securestorage.StartKeychainPrefetchProcess(ctx, command)
		if err == nil {
			started <- process
		}
		return process, err
	}
	parsed := cliargs.Parse([]string{"mcp", "list"}, cliargs.Config{Version: version})
	if parsed.Parsed == nil {
		t.Fatal(parsed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		code                int
		err                 error
		output, diagnostics string
	}
	done := make(chan outcome, 1)
	go func() {
		var output, diagnostics bytes.Buffer
		code, err := runNativeMCPCommand(ctx, []string{"mcp", "list"}, *parsed.Parsed, n, nil, &output, &diagnostics, "darwin")
		done <- outcome{code, err, output.String(), diagnostics.String()}
	}()
	processes := []*securestorage.KeychainPrefetchProcess{}
	for len(processes) < 2 {
		select {
		case process := <-started:
			processes = append(processes, process)
		case <-time.After(5 * time.Second):
			t.Fatal("two actual startup children did not launch")
		}
	}
	select {
	case result := <-done:
		t.Fatal("actual public caller escaped before held preAction completion", result)
	default:
	}
	cancel()
	select {
	case result := <-done:
		if result.code != 1 || !errors.Is(result.err, context.Canceled) || result.output != "" || result.diagnostics != "" {
			t.Fatal("actual caller did not retain startup cancellation", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("actual CLI caller did not cancel and join its security children")
	}
	for _, process := range processes {
		select {
		case <-process.Done():
		default:
			t.Fatal("CLI returned before actual child Wait", process.PID())
		}
	}
}
