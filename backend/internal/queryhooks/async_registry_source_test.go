package queryhooks

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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type asyncPrimitiveFixture struct {
	id, status, stdout, stderr string
	code                       int
	cleanup, killed            *[]string
	done                       chan struct{}
}

func (p *asyncPrimitiveFixture) Snapshot() (CommandOutput, string, error) {
	return CommandOutput{Stdout: p.stdout, Stderr: p.stderr, Output: p.stdout + p.stderr, Code: p.code}, p.status, nil
}
func (p *asyncPrimitiveFixture) Done() <-chan struct{} { return p.done }
func (p *asyncPrimitiveFixture) Kill() {
	*p.killed = append(*p.killed, p.id)
	p.status = "killed"
	select {
	case <-p.done:
	default:
		close(p.done)
	}
}
func (p *asyncPrimitiveFixture) Cleanup() { *p.cleanup = append(*p.cleanup, p.id) }
func (p *asyncPrimitiveFixture) Join()    { <-p.done }

func TestActualSourceAsyncRegistryCompletionOrderingParsingAndShutdown(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	home := t.TempDir()
	profiles := []map[string]any{
		{"hooks": []map[string]any{{"id": "running", "status": "running", "stdout": "partial", "stderr": "", "code": 0}, {"id": "complete", "status": "completed", "stdout": "{\"async\":true}\n{\"systemMessage\":\"old\",\"future\":true}\n{\"systemMessage\":\"later\"}\n", "stderr": "error", "code": 2}}, "finalize": true},
		{"hooks": []map[string]any{{"id": "empty", "status": "completed", "stdout": " \n", "stderr": "error", "code": 1}, {"id": "killed", "status": "killed", "stdout": "orphan", "stderr": "", "code": 137}, {"id": "invalid", "status": "completed", "stdout": "broken\n{wrong}\n{\"async\":false,\"systemMessage\":\"skip\"}\n{\"hookSpecificOutput\":{\"additionalContext\":\"next\"}}\n", "stderr": "", "code": 0}}},
		{"hooks": []map[string]any{{"id": "fresh", "status": "completed", "event": "SessionStart", "stdout": "{\"systemMessage\":\"text\\ud800\",\"unknown\":{\"keep\":true}}\n", "stderr": "", "code": 0, "timeout": -1, "toolName": "Write", "pluginId": "private"}, {"id": "fresh2", "status": "completed", "event": "SessionStart", "stdout": "plain\n", "stderr": "", "code": 0, "timeout": 0}}},
		{"hooks": []map[string]any{{"id": "completed-final", "status": "completed", "stdout": "", "stderr": "x", "code": 4}, {"id": "background", "status": "backgrounded", "stdout": "still", "stderr": "", "code": 0}}, "finalize": true},
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-async-hook-registry.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_ASYNC_HOOK_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(commandJSON(map[string]any{"rows": profiles}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Rows []json.RawMessage }
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	for i, profile := range profiles {
		owner := cooperative.New()
		events := []protocol.JSONObject{}
		cleanup, killed := []string{}, []string{}
		invalidations := 0
		registry, err := NewAsyncRegistry(AsyncRegistryOptions{Owner: owner, NowMillis: func() float64 { return 123 }, Notify: func(*cooperative.Scope, string) error { return nil }, Invalidate: func() { invalidations++ }, Event: func(_ *cooperative.Scope, h AsyncHook, o CommandOutput, outcome string) error {
			events = append(events, protocol.NewJSONObject(protocol.JSONField{Name: "hookId", Value: commandJSON(h.HookID)}, protocol.JSONField{Name: "hookName", Value: commandJSON(h.HookName)}, protocol.JSONField{Name: "hookEvent", Value: commandJSON(h.HookEvent)}, protocol.JSONField{Name: "output", Value: commandJSON(o.Stdout + o.Stderr)}, protocol.JSONField{Name: "stdout", Value: commandJSON(o.Stdout)}, protocol.JSONField{Name: "stderr", Value: commandJSON(o.Stderr)}, protocol.JSONField{Name: "exitCode", Value: commandJSON(o.Code)}, protocol.JSONField{Name: "outcome", Value: commandJSON(outcome)}))
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		before := []map[string]any{}
		pending := []string{}
		responses := []protocol.JSONObject{}
		err = owner.Run(ctx, func(scope *cooperative.Scope) error {
			for _, row := range profile["hooks"].([]map[string]any) {
				id := row["id"].(string)
				p := &asyncPrimitiveFixture{id: id, status: row["status"].(string), stdout: row["stdout"].(string), stderr: row["stderr"].(string), code: row["code"].(int), cleanup: &cleanup, killed: &killed, done: make(chan struct{})}
				if p.status == "completed" || p.status == "killed" {
					close(p.done)
				}
				h := AsyncHook{ProcessID: id, HookID: "hook-" + id, HookName: "PostToolUse:Write", HookEvent: "PostToolUse", Command: "private-command", Process: p}
				if v, ok := row["event"].(string); ok {
					h.HookEvent = v
				}
				if v, ok := row["timeout"].(int); ok {
					h.Timeout = float64(v)
				}
				if v, ok := row["toolName"].(string); ok {
					h.ToolName = v
				}
				if v, ok := row["pluginId"].(string); ok {
					h.PluginID = v
				}
				if err := registry.Register(scope, h); err != nil {
					return err
				}
			}
			rows, err := registry.Pending(scope)
			if err != nil {
				return err
			}
			for _, h := range rows {
				before = append(before, map[string]any{"processId": h.ProcessID, "timeout": h.Timeout, "startTime": h.StartTime})
			}
			responses, err = registry.Responses(scope)
			if err != nil {
				return err
			}
			rows, err = registry.Pending(scope)
			for _, h := range rows {
				pending = append(pending, h.ProcessID)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if profile["finalize"] == true {
			if err = registry.Close(ctx); err != nil {
				t.Fatal(err)
			}
		}
		got := commandJSON(map[string]any{"before": before, "responses": responses, "pending": pending, "events": events, "cleanup": cleanup, "killed": killed, "invalidations": invalidations})
		var a, b any
		_ = json.Unmarshal(got, &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		// Progress timer ordering follows separate batches; compare membership.
		delete(b.(map[string]any), "stopped")
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case%d Go%s Source%s", i, got, source.Rows[i])
		}
		_ = registry.Close(ctx)
		owner.Close()
	}
}
