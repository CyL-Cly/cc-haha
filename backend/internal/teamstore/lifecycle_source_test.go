package teamstore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
)

func TestPhysicalTerminalCleanupIncarnationGuardsAndRetryMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	kinds := []string{"normal", "inferred", "changed", "stale", "retry", "absent"}
	cases := []map[string]string{}
	for _, kind := range kinds {
		cases = append(cases, map[string]string{"kind": kind})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-cleanup.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_CLEANUP_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "cases": cases}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(kinds) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	for i, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			configDir := filepath.Join(t.TempDir(), ".claude")
			tasks, _ := taskstore.New(taskstore.Options{ConfigDir: configDir, PID: os.Getpid(), Now: func() time.Time { return time.UnixMilli(2000) }})
			config, _ := NewConfig(ConfigOptions{ConfigDir: configDir, TaskStore: tasks, PID: os.Getpid()})
			cleanup, err := NewLifecycle(LifecycleOptions{Config: config, Tasks: tasks, RuntimeResources: func(_ context.Context, _ string, members []protocol.JSONObject, kill bool) error {
				if len(members) != 0 || kill {
					t.Error("unexpected resource cleanup", members, kill)
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			name := "cleanup-" + kind
			list := SanitizeName(name)
			team := object(raw(map[string]any{"name": name, "createdAt": 1000, "leadSessionId": "private-leader", "leadAgentId": "team-lead@" + name, "members": []any{}}))
			var expected *taskstore.LifecycleToken
			if kind != "absent" {
				if err = config.Write(name, team); err != nil {
					t.Fatal(err)
				}
				v, err := tasks.BeginLifecycle(list, identity(team))
				if err != nil {
					t.Fatal(err)
				}
				expected = &v
				if _, err = tasks.Create(list, object(raw(map[string]any{"subject": "Actual terminal task", "description": "Preserved", "status": "pending", "blocks": []string{}, "blockedBy": []string{}, "metadata": map[string]bool{"unknown": true}}))); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "changed" {
				team.Set("createdAt", raw(1001))
				_ = config.Write(name, team)
			}
			if kind == "stale" {
				expected.Generation--
			}
			input := expected
			if kind == "inferred" {
				input = nil
			}
			receipt, failure := cleanup.Cleanup(context.Background(), name, input, false)
			if failure == nil && kind == "retry" {
				receipt, failure = cleanup.Cleanup(context.Background(), name, expected, false)
			}
			var result, message any
			if failure == nil {
				result = receipt
			} else {
				message = failure.Error()
			}
			physicalTasks, err := tasks.List(list)
			if err != nil {
				t.Fatal(err)
			}
			state, err := tasks.ReadLifecycle(list)
			if err != nil {
				t.Fatal(err)
			}
			actual := raw(map[string]any{"result": result, "error": message, "config": config.Read(name), "tasks": physicalTasks, "lifecycle": state})
			var a, b any
			_ = json.Unmarshal(actual, &a)
			_ = json.Unmarshal(source.Rows[i], &b)
			if !reflect.DeepEqual(a, b) {
				t.Errorf("Go%s\nSource%s", actual, source.Rows[i])
			}
		})
	}
}
