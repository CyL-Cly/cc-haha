package application

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDesktopTeamFallbackReadsRealPrivatePreferenceAndPreservesUserJSON(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "PATH": "/usr/bin:/bin", "CC_HAHA_DISABLE_TERMINAL_SHELL_ENV": "true", "CC_HAHA_AGENT_TEAMS_ENABLED": "stale", "CLAUDE_CODE_ENABLE_TASKS": "0"}
	for _, enabled := range []bool{false, true, false} {
		before := raw(protocol.NewJSONObject(protocol.JSONField{Name: "agentTeamsEnabled", Value: raw(enabled)}, protocol.JSONField{Name: "future", Value: []byte(`{"keep":"\ud800"}`)}))
		if err := os.WriteFile(filepath.Join(config, "settings.json"), before, 0600); err != nil {
			t.Fatal(err)
		}
		got, err := desktopTeamEnvironment(context.Background(), Options{desktopTeamLaunch: &desktopTeamLaunch{}, WorkDir: home}, env)
		if err != nil {
			t.Fatal(err)
		}
		want := "0"
		if enabled {
			want = "1"
		}
		if got["CC_HAHA_AGENT_TEAMS_ENABLED"] != want || got["CLAUDE_CODE_ENABLE_TASKS"] != "1" || got["CC_HAHA_TEAM_REVIEW_REQUIRED"] != "1" {
			t.Fatalf("actual preference consumer mismatch %v", got)
		}
		after, err := os.ReadFile(filepath.Join(config, "settings.json"))
		if err != nil || !bytes.Equal(after, before) {
			t.Fatalf("getter changed user JSON %v %s", err, after)
		}
	}
	if env["CC_HAHA_AGENT_TEAMS_ENABLED"] != "stale" || env["CLAUDE_CODE_ENABLE_TASKS"] != "0" {
		t.Fatal("mutated inherited process environment")
	}
}
