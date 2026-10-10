package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentProgressStateAndSummaryAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	m := taskMainFixture(t)
	home := m.options.Native.Home
	_ = os.MkdirAll(m.options.Native.Temp, 0700)
	if e := os.MkdirAll(filepath.Join(home, ".git"), 0700); e != nil {
		t.Fatal(e)
	}
	rows := []string{
		`{"message":{"type":"user","message":{"role":"user","content":"original"}}}`,
		`{"message":{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"output_tokens":1},"content":[{"type":"tool_use","id":"1","name":"Read","input":{"file_path":"one.txt"}},{"type":"tool_use","id":"2","name":"StructuredOutput","input":{"result":"private"}},{"type":"tool_use","id":"3","name":"Glob","input":{"pattern":"*.go"}},{"type":"tool_use","id":"4","name":"Write","input":{"file_path":"ordinary.txt","content":"contents"}},{"type":"tool_use","id":"5","name":"mcp__fixture__tool","input":{"value":1}},{"type":"tool_use","id":"6","name":"Grep","input":{"pattern":"term"}}]}}}`,
		`{"message":{"type":"assistant","message":{"usage":{"input_tokens":20,"output_tokens":3},"content":[{"type":"text","text":"private live partial"}]}}}`,
		`{"summary":"original summary preserved","message":{"type":"assistant","message":{"usage":{"input_tokens":25,"cache_read_input_tokens":3,"output_tokens":2},"content":[{"type":"tool_use","id":"7","name":"Bash","input":{"command":"cat file | grep term","description":"Read actual fixture"}}]}}}`,
		`{"summary":"","message":{"type":"user","message":{"role":"user","content":"tool result"}}}`,
		`{"status":"completed","message":{"type":"assistant","message":{"usage":{"input_tokens":32,"output_tokens":1},"content":[{"type":"tool_use","id":"8","name":"PowerShell","input":{"command":"CAT file"}}]}}}`,
	}
	steps := []protocol.JSONObject{}
	for _, row := range rows {
		steps = append(steps, taskObject(t, []byte(row)))
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-progress.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + m.options.Native.Temp, "CLAUDE_CODE_TMPDIR=" + m.options.Native.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_AGENT_PROGRESS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "cwd": m.options.Native.CWD, "steps": steps})
	cmd.Stdin = bytes.NewReader(data)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.5500s", e, out)
	}
	var source struct {
		Progress     []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Progress) != len(steps) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.5500s", e, out)
	}
	p := m.nativeAgentProgress(builtintools.AgentRequest{ParentTools: []string{"Bash", "Read", "Grep", "Glob", "Write", "Edit", "PowerShell"}}, m.options.Native.CWD)
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		id := "a0123456789abcdef"
		_, cancelTask := context.WithCancelCause(context.Background())
		defer cancelTask(nil)
		task := taskObject(t, []byte(`{"id":"a0123456789abcdef","type":"local_agent","status":"running"}`))
		if e := m.nativeBackgroundBindings().Register(scope, &sessioncontroller.BackgroundTask{Data: task, Cancel: cancelTask, ReadOutput: func(context.Context) (json.RawMessage, error) { return quoteMain(""), nil }, Join: func(context.Context) error { return nil }}); e != nil {
			return e
		}
		for i, step := range steps {
			if step.Get("summary") != nil {
				if e := m.nativeBackgroundBindings().Update(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
					progress, _ := task.Object("progress")
					if !progress.IsObject() {
						progress = protocol.NewJSONObject()
					}
					progress.Set("summary", step.Get("summary"))
					task.Set("progress", rawMain(progress))
					return task, nil
				}); e != nil {
					return e
				}
			}
			if step.Get("status") != nil {
				if e := m.nativeBackgroundBindings().Update(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
					task.Set("status", step.Get("status"))
					return task, nil
				}); e != nil {
					return e
				}
			}
			if message := step.Get("message"); message != nil {
				p.observe(message)
				if p.failure != nil {
					return p.failure
				}
				if e := m.updateNativeAgentProgress(scope, id, p); e != nil {
					return e
				}
			}
			task, e := m.nativeBackgroundBindings().Lookup(scope, id)
			if e != nil {
				return e
			}
			got := protocol.NewJSONObject(protocol.JSONField{Name: "tracker", Value: rawMain(p.snapshot())}, protocol.JSONField{Name: "task", Value: rawMain(task.Data)})
			if string(protocol.StringifyValidatedJavaScriptJSON(rawMain(got))) != string(protocol.StringifyValidatedJavaScriptJSON(rawMain(source.Progress[i]))) {
				t.Errorf("step%d\nGo%s\nSource%s", i, rawMain(got), rawMain(source.Progress[i]))
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
