//go:build !offline_native_fixture && !native_process_fixture

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOrdinaryAgentsFactoryUsesTheProductionNativeBoundary(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err = os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "DISABLE_TELEMETRY": "1", "ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic", "ANTHROPIC_AUTH_TOKEN": "fake-only", "OPENAI_CODEX_OAUTH_FILE": "", "CLAUDE_CODE_OAUTH_TOKEN": "", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST": "", "CC_HAHA_PROXY_SERVER_PORT": ""} {
		t.Setenv(key, value)
	}
	native, err := newMainAgentsNativeBoundary()
	if err != nil {
		t.Fatal(err)
	}
	if !native.NativeProcess || native.ExternalAbsent || native.Home != home || native.Environment["CLAUDE_CONFIG_DIR"] != filepath.Join(home, ".claude") || native.ResolvePolicy == nil {
		t.Fatal("ordinary agents lacks the actual private native startup bindings")
	}
}
