package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceAgentHookPriorityInputFailureAndParentScope(t *testing.T) {
	home := t.TempDir()
	sid := "60600000-0000-4000-8000-000000000606"
	fixture, err := filepath.Abs("../../testdata/native-agent-isolation-hook.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AGENT_ISOLATION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"rows": []any{map[string]any{"name": "success", "sid": sid, "slug": "agent-a1234567"}, map[string]any{"name": "failed", "sid": sid, "slug": "agent-b1234567", "fail": true}}}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source hook", err, stderr.String())
	}
	var source struct {
		Rows []struct {
			Name, Target, Error string
			Event               protocol.JSONObject
			Result              struct {
				Worktree struct {
					Path      string `json:"worktreePath"`
					HookBased bool   `json:"hookBased"`
				}
			}
		}
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != 2 {
		t.Fatal("actual Source receipt", err)
	}
	for _, row := range source.Rows {
		f := newNativePluginFixture(t)
		f.n.Environment["ANTHROPIC_API_KEY"] = "fake-source-hook-606"
		f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
		cwd := row.Event.String("cwd")
		f.n.CWD = cwd
		script := filepath.Join(home, row.Name, "create.py")
		settings := map[string]any{"enabledPlugins": map[string]bool{"sample@mkt": false}, "agentTeamsEnabled": false, "hooks": map[string]any{"WorktreeCreate": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/python3 " + script}}}}}}
		f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(settings)))
		var diagnostics strings.Builder
		m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, SessionID: sid, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: &diagnostics, Error: &diagnostics})
		if err != nil {
			t.Fatal(err)
		}
		if err = m.StartSession(ctx); err != nil {
			t.Fatal(err)
		}
		var result *nativeAgentWorktree
		err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			var e error
			result, e = m.prepareAgentWorktree(ctx, scope, builtintools.AgentRequest{Isolation: "worktree"}, row.Event.String("name")[6:])
			return e
		})
		if row.Error != "" {
			if err == nil || err.Error() != row.Error {
				t.Fatal("native/Source exact hook failure", err, row.Error)
			}
		} else if err != nil || result == nil || result.Info == nil || !result.Info.HookBased || result.Info.Path != row.Result.Worktree.Path {
			t.Fatal("native/Source hook-based result", result, err)
		}
		input, readErr := os.ReadFile(filepath.Join(home, row.Name, "journal.json"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		var event protocol.JSONObject
		_ = event.UnmarshalJSON(input)
		for _, key := range []string{"session_id", "cwd", "hook_event_name", "name"} {
			if event.String(key) != row.Event.String(key) {
				t.Fatal("native/Source physical hook input", key, event.String(key), row.Event.String(key))
			}
		}
		if m.state.Snapshot().CWD != cwd || m.worktreeState != nil && m.worktreeState.current != nil {
			t.Fatal("Agent hook changed session worktree state")
		}
		if err = m.ShutdownSession(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
