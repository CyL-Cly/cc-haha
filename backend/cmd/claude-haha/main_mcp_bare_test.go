package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestMCPCLIListBareAndForcedTokenSkipSecureCredentials(t *testing.T) {
	for _, profile := range []string{"flag-bare", "env-simple", "forced-oauth", "disabled-claudeai"} {
		t.Run(profile, func(t *testing.T) {
			n, _ := mcpCLIPrivateNative(t)
			// This test exercises MCP token selection, independently of the
			// still-guarded first-party managed-policy prefetch producer.
			n.Environment["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] = "1"
			var secureReads atomic.Int32
			n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
				secureReads.Add(1)
				return nil, errors.New("isolated unavailable Keychain")
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
			if secureReads.Load() != 0 {
				t.Fatalf("Source auth branch must skip secure reads; got %d", secureReads.Load())
			}
		})
	}
}
