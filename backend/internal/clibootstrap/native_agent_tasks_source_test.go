package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func taskSemanticEqual(a, b json.RawMessage) bool {
	var left, right any
	_ = json.Unmarshal(a, &left)
	_ = json.Unmarshal(b, &right)
	return reflect.DeepEqual(left, right)
}

func TestActualForegroundAndBackgroundAgentTaskStateProgressAndNotificationsMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	m := taskMainFixture(t)
	if e := os.MkdirAll(m.options.Native.Temp, 0700); e != nil {
		t.Fatal(e)
	}
	const now = 1700000000000.0
	m.options.Native.NowMillis = func() float64 { return now }
	cases := []protocol.JSONObject{}
	for i, status := range []string{"foreground", "completed", "failed", "killed", "completed"} {
		id := []string{"asourcefg", "asourcecomplete", "asourcefail", "asourcekill", "asourceowned"}[i]
		selected := protocol.NewJSONObject(protocol.JSONField{Name: "agentType", Value: quoteMain("general-purpose")}, protocol.JSONField{Name: "tools", Value: rawMain([]string{"Bash", "Read"})}, protocol.JSONField{Name: "description", Value: quoteMain("Original selected definition")})
		row := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: quoteMain(id)}, protocol.JSONField{Name: "description", Value: quoteMain("Original Agent <summary>&")}, protocol.JSONField{Name: "prompt", Value: quoteMain("Original Agent prompt")}, protocol.JSONField{Name: "selectedAgent", Value: rawMain(selected)}, protocol.JSONField{Name: "background", Value: rawMain(status != "foreground")}, protocol.JSONField{Name: "status", Value: quoteMain(status)}, protocol.JSONField{Name: "toolUseId", Value: quoteMain("original-agent-use")}, protocol.JSONField{Name: "now", Value: rawMain(now)})
		messages := []protocol.JSONObject{taskObject(t, []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"UnknownSourceTool","id":"original-inner","input":{}}],"usage":{"input_tokens":3,"cache_creation_input_tokens":4,"cache_read_input_tokens":5,"output_tokens":7}}}`)), taskObject(t, []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Original final result <exact>&"}],"usage":{"input_tokens":11,"cache_creation_input_tokens":13,"cache_read_input_tokens":17,"output_tokens":19}}}`))}
		row.Set("messages", rawMain(messages))
		if status == "failed" {
			row.Set("error", quoteMain("Original failure"))
		}
		if i == 4 {
			row.Set("ownerAgentId", quoteMain("actual-parent-agent"))
		}
		result := protocol.NewJSONObject(protocol.JSONField{Name: "agentId", Value: quoteMain(id)}, protocol.JSONField{Name: "agentType", Value: quoteMain("general-purpose")}, protocol.JSONField{Name: "content", Value: rawMain([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("text")}, protocol.JSONField{Name: "text", Value: quoteMain("Original final result <exact>&")})})}, protocol.JSONField{Name: "totalDurationMs", Value: rawMain(0)}, protocol.JSONField{Name: "totalTokens", Value: rawMain(60)}, protocol.JSONField{Name: "totalToolUseCount", Value: rawMain(1)}, protocol.JSONField{Name: "usage", Value: messages[1].Get("message")})
		last, _ := messages[1].Object("message")
		result.Set("usage", last.Get("usage"))
		row.Set("result", rawMain(result))
		if status == "completed" {
			row.Set("finalMessage", quoteMain("Original final result <exact>&"))
			row.Set("usage", rawMain(map[string]any{"totalTokens": 60, "toolUses": 1, "durationMs": 0}))
		}
		cases = append(cases, row)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-background-tasks.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	home := m.options.Native.Home
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + m.options.Native.Temp, "CLAUDE_CODE_TMPDIR=" + m.options.Native.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_BACKGROUND_TASK_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cwd": m.options.Native.CWD, "sessionId": m.Controller.SessionID(), "agents": cases})
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.2500s", e, out)
	}
	var source struct {
		AgentCases []struct {
			Initial, Terminal                          protocol.JSONObject
			Events, Published, TerminalEvents, Pending []protocol.JSONObject
		}
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.AgentCases) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.2500s", e, out)
	}
	for i, row := range cases {
		t.Run(row.String("id"), func(t *testing.T) {
			frames := []protocol.JSONObject{}
			m.options.OnOutput = func(data []byte) error {
				value := taskObject(t, data)
				value.Delete("uuid")
				value.Delete("session_id")
				frames = append(frames, value)
				return nil
			}
			if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				m.options.AgentID = row.String("ownerAgentId")
				id := row.String("id")
				request := builtintools.AgentRequest{AgentType: "general-purpose", Prompt: row.String("prompt"), Description: row.String("description"), SelectedAgent: taskObject(t, row.Get("selectedAgent")), ToolUseID: row.Get("toolUseId")}
				path := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(m.options.Native.CWD), m.Controller.SessionID(), "subagents", "agent-"+id+".jsonl")
				if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
					return e
				}
				if e := os.WriteFile(path, []byte("{\"original\":\"transcript\"}\n"), 0600); e != nil {
					return e
				}
				taskContext, taskCancel := context.WithCancelCause(context.Background())
				defer taskCancel(nil)
				done := make(chan struct{})
				defer close(done)
				if e := m.beginNativeAgentTask(scope, id, path, request, taskCancel, done, string(row.Get("background")) == "true"); e != nil {
					return e
				}
				entry, e := m.Controller.BackgroundTaskOnOwner(scope, id)
				if e != nil {
					return e
				}
				if !taskSemanticEqual(rawMain(entry.Data), rawMain(source.AgentCases[i].Initial)) {
					t.Errorf("initial Go%s Source%s", rawMain(entry.Data), rawMain(source.AgentCases[i].Initial))
				}
				progress := &foregroundAgentProgress{}
				var messages []protocol.JSONObject
				_ = json.Unmarshal(row.Get("messages"), &messages)
				history := []queryloop.Message{}
				for _, message := range messages {
					history = append(history, queryloop.Message{Record: message})
					if last := progress.observe(rawMain(message)); last != "" {
						if e = m.emitForegroundAgentProgress(scope, id, request, now, progress, last); e != nil {
							return e
						}
					}
				}
				if !taskSemanticEqual(rawMain(frames), rawMain(source.AgentCases[i].Events)) {
					t.Errorf("events Go%s Source%s", rawMain(frames), rawMain(source.AgentCases[i].Events))
				}
				frames = nil
				if row.String("status") == "foreground" {
					if e = m.endForegroundAgentTask(scope, id, request, now, nil, progress); e != nil {
						return e
					}
					entry, e = m.Controller.BackgroundTaskOnOwner(scope, id)
					if e != nil {
						return e
					}
					if entry != nil || source.AgentCases[i].Terminal.IsObject() {
						t.Fatal("foreground task not unregistered")
					}
					return nil
				}
				var cause error
				if row.String("status") == "failed" {
					cause = errors.New(row.String("error"))
				}
				if row.String("status") == "killed" {
					if _, e = m.Controller.StopBackgroundTaskOnOwner(scope, id, now); e != nil {
						return e
					}
					cause = context.Cause(taskContext)
				}
				if e = m.completeNativeBackgroundAgent(scope, id, request, builtintools.AgentRunResult{AgentID: id, Messages: history, DurationMillis: 0}, cause, progress, now, nil); e != nil {
					return e
				}
				entry, e = m.Controller.BackgroundTaskOnOwner(scope, id)
				if e != nil {
					return e
				}
				if !taskSemanticEqual(rawMain(entry.Data), rawMain(source.AgentCases[i].Terminal)) {
					t.Errorf("terminal Go%s Source%s", rawMain(entry.Data), rawMain(source.AgentCases[i].Terminal))
				}
				expected := append(source.AgentCases[i].Published, source.AgentCases[i].TerminalEvents...)
				if !taskSemanticEqual(rawMain(frames), rawMain(expected)) {
					t.Errorf("notifications Go%s Source%s", rawMain(frames), rawMain(expected))
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
