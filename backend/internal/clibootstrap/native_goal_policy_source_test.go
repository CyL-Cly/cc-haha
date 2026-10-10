package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestNativeGoalPolicyAndTrustNoModelCommandAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	for _, test := range []struct {
		name                           string
		disabled, interactive, trusted bool
	}{{"disabled", true, false, false}, {"untrusted", false, true, false}, {"trusted", false, true, true}, {"SDK implicit trust", false, false, false}} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			fixture, _ := filepath.Abs("../goalstate/testdata/goal-command.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_GOAL_COMMAND_FIXTURE=1", "PATH=/usr/bin:/bin", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
			cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "arguments": []string{"do real work"}, "interactive": test.interactive, "trusted": test.trusted, "settings": map[string]any{"disableAllHooks": test.disabled}}))
			data, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(data))
			}
			var source struct {
				Rows []struct {
					Messages    []protocol.JSONObject
					ShouldQuery bool
				}
			}
			if err = json.Unmarshal(data, &source); err != nil {
				t.Fatal(err, string(data))
			}
			f := worktreeMainFixture(t)
			settings, _ := json.Marshal(map[string]any{"agentTeamsEnabled": false, "alwaysThinkingEnabled": false, "disableAllHooks": test.disabled, "enabledPlugins": map[string]bool{"sample@mkt": false}})
			f.write(t, filepath.Join(f.config, "settings.json"), string(settings))
			global, _ := json.Marshal(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{f.cwd: map[string]any{"hasTrustDialogAccepted": test.trusted}}})
			f.write(t, filepath.Join(f.config, ".claude.json"), string(global))
			disabled := false
			m, err := NewMainPrint(MainPrintOptions{ForegroundShellProgress: test.interactive, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual no-model goal policy", Tools: []string{"Read"}, MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
			if err = m.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				text, query, err := m.nativeGoalCommand(ctx, scope, "do real work")
				if err != nil {
					return err
				}
				expected := source.Rows[0].Messages[1].String("content")
				if expected != "<local-command-stdout>"+text+"</local-command-stdout>" || query != source.Rows[0].ShouldQuery {
					t.Errorf("Go%q query%v Source%s", text, query, data)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
