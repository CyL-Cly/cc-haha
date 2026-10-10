package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

func TestActualMainTeammateStopTasksThenIdleAndInputMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	rows := []map[string]any{
		{"mode": "bypassPermissions", "tasks": []any{}},
		{"mode": "bypassPermissions", "tasks": []any{map[string]any{"subject": "owned", "description": "actual completed hook", "status": "in_progress", "owner": "worker", "blocks": []string{}, "blockedBy": []string{}}}},
		{"mode": "bypassPermissions", "tasks": []any{map[string]any{"subject": "other", "description": "not ours", "status": "in_progress", "owner": "elsewhere", "blocks": []string{}, "blockedBy": []string{}}, map[string]any{"subject": "owned-two", "description": "actual", "status": "in_progress", "owner": "worker", "blocks": []string{}, "blockedBy": []string{}}, map[string]any{"subject": "finished", "description": "not in progress", "status": "completed", "owner": "worker", "blocks": []string{}, "blockedBy": []string{}}}},
	}
	actual := []any{}
	for i, row := range rows {
		team := fmt.Sprintf("actual-stop-%d", i)
		names := []string{"Read"}
		m, f, _ := teamWorkerFixture(t, teamworker.Start{ParentSessionID: "parent", TeamName: team, MemberID: "m", Name: "worker", SystemPrompt: "actual", Tools: &names, AgentDefinition: protocol.NewJSONObject()}, "http://127.0.0.1:1")
		delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
		if err := m.StartSession(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, task := range row["tasks"].([]any) {
			if _, err := m.taskStore.Create(team, objMain(rawMain(task))); err != nil {
				t.Fatal(err)
			}
		}
		path := filepath.Join(f.cwd, "actual-stop-order.jsonl")
		command := "while IFS= read -r line; do echo \"$line\" >> '" + strings.ReplaceAll(path, "'", "'\\''") + "'; done"
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			hooks := protocol.NewJSONObject()
			for _, event := range []string{"TaskCompleted", "TeammateIdle"} {
				hooks.Set(event, rawMain([]any{map[string]any{"hooks": []any{map[string]string{"type": "command", "command": command}}}}))
			}
			if err := m.hook.RegisterNativeMainHooks(m.Controller.SessionID(), hooks); err != nil {
				return err
			}
			_, err := m.hook.StopHooks(ctx, queryhooks.Context{Scope: scope, ComputerUseInactive: true, GoalInactive: true, TeammateInactive: false, TeammateStop: m.nativeTeammateStopBinding()}, func(queryloop.Yield) error { return nil })
			return err
		})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		calls := []any{}
		for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
			v := objMain(line)
			if v.String("session_id") != m.Controller.SessionID() || v.Get("agent_id") != nil || v.String("agent_type") != "worker" {
				t.Fatalf("wrong MAIN task input %s", line)
			}
			call := protocol.NewJSONObject(protocol.JSONField{Name: "event", Value: v.Get("hook_event_name")}, protocol.JSONField{Name: "id", Value: v.Get("task_id")}, protocol.JSONField{Name: "subject", Value: v.Get("task_subject")}, protocol.JSONField{Name: "description", Value: v.Get("task_description")}, protocol.JSONField{Name: "name", Value: v.Get("teammate_name")}, protocol.JSONField{Name: "team", Value: v.Get("team_name")}, protocol.JSONField{Name: "mode", Value: v.Get("permission_mode")})
			calls = append(calls, call)
		}
		actual = append(actual, calls)
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-stop-order.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_STOP_ORDER_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var source struct {
		ProgressLoggable bool
		Observations     []struct{ Calls json.RawMessage }
		Capabilities     struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Observations) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output), stderr.String())
	}
	if source.ProgressLoggable {
		t.Fatal("actual Source changed hook progress persistence")
	}
	for i, row := range actual {
		var a, b any
		_ = json.Unmarshal(rawMain(row), &a)
		_ = json.Unmarshal(source.Observations[i].Calls, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case%d Go%s Source%s", i, rawMain(row), source.Observations[i].Calls)
		}
	}
}
