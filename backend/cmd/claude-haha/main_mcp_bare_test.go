package main

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestMCPCLIListBareAndForcedTokenSkipSecureCredentials(t *testing.T) {
	for _, profile := range []string{"flag-bare", "env-simple", "forced-oauth", "disabled-claudeai"} {
		t.Run(profile, func(t *testing.T) {
			n, _ := mcpCLIPrivateNative(t)
			// This test exercises MCP token selection, independently of the
			// independent startup prefetch and advisor consumers.
			n.Environment["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] = "1"
			var secureReads atomic.Int32
			var catalogReads atomic.Int32
			n.RunSecurity = func(_ context.Context, call securestorage.Command) ([]byte, error) {
				if call.ShellScript != "" {
					secureReads.Add(1)
				}
				// Count the actual credential caller, not every now-reachable
				// preAction migration as if it belonged to MCP token selection.
				// Whole startup totals are retained and measured separately
				// against original main, including its two prefetch primitives.
				pcs := make([]uintptr, 32)
				count := runtime.Callers(1, pcs)
				if count == len(pcs) {
					t.Error("credential caller stack was truncated; cannot classify catalog reads")
				}
				frames := runtime.CallersFrames(pcs[:count])
				for {
					frame, more := frames.Next()
					if filepath.Base(frame.File) == "main_command_claudeai_mcp.go" {
						catalogReads.Add(1)
						break
					}
					if !more {
						break
					}
				}
				return nil, errors.New("isolated unavailable Keychain")
			}
			originalStart := n.StartKeychainPrefetch
			n.StartKeychainPrefetch = func(ctx context.Context, call securestorage.Command) (*securestorage.KeychainPrefetchProcess, error) {
				secureReads.Add(1)
				return originalStart(ctx, call)
			}
			args := []string{"mcp", "list"}
			switch profile {
			case "flag-bare":
				args = []string{"--bare", "mcp", "list"}
			case "env-simple":
				n.Environment["CLAUDE_CODE_SIMPLE"] = "1"
			case "forced-oauth":
				n.Environment["CLAUDE_CODE_OAUTH_TOKEN"] = "private-inference-only"
			case "disabled-claudeai":
				n.Environment["ENABLE_CLAUDEAI_MCP_SERVERS"] = "false"
			}
			code, out, diagnostics, err := mcpCLIExecute(t, n, args)
			if code != 0 || err != nil || diagnostics != "" {
				t.Fatal(code, err, out, diagnostics)
			}
			if catalogReads.Load() != 0 {
				t.Fatalf("Source MCP auth branch must skip credential reads; catalog=%d totalStartup=%d", catalogReads.Load(), secureReads.Load())
			}
			t.Logf("actual full startup reads=%d; actual MCP catalog reads=%d; prefetch parity is a separate whole-main consumer", secureReads.Load(), catalogReads.Load())
		})
	}
}
