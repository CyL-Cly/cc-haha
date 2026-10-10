package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNativeCommandEventsUseActualOwnerInputProcessOutputAndFactory(t *testing.T) {
	var journal string
	m := backgroundAgentMainWithSetup(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		journal = filepath.Join(n.CWD, "actual-event-inputs.jsonl")
		script := filepath.Join(n.CWD, "events.py")
		code := fmt.Sprintf("import sys,json\nx=json.load(sys.stdin)\nwith open(%q,'a') as f:f.write(json.dumps(x)+'\\n')\ns=x['hook_event_name']\nprint(json.dumps({'hookSpecificOutput':({'hookEventName':s,'permissionDecision':'allow','updatedInput':{'file_path':'changed-by-hook'},'additionalContext':'actual context'} if s=='PreToolUse' else {'hookEventName':s,'additionalContext':'actual post context'})}))\n", journal)
		if e := os.WriteFile(script, []byte(code), 0600); e != nil {
			t.Fatal(e)
		}
		hooks := map[string]any{}
		for _, event := range []string{"PreToolUse", "PostToolUse"} {
			hooks[event] = []any{map[string]any{"matcher": "Read", "hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/python3 " + strconv.Quote(script), "statusMessage": "actual " + event}}}, map[string]any{"matcher": "unmatched-tool", "hooks": []any{map[string]any{"type": "agent", "prompt": "must not run"}}}}
		}
		if e := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(map[string]any{"hooks": hooks}), 0600); e != nil {
			t.Fatal(e)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, event := range []string{"PreToolUse", "PostToolUse"} {
		results := []queryhooks.EventResult{}
		if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			fields := protocol.NewJSONObject(protocol.JSONField{Name: "tool_name", Value: quoteMain("Read")}, protocol.JSONField{Name: "tool_input", Value: rawMain(map[string]any{"file_path": "actual-original"})}, protocol.JSONField{Name: "tool_use_id", Value: quoteMain("actual-event-use")})
			if event == "PostToolUse" {
				fields.Set("tool_response", rawMain(map[string]any{"data": "actual prior read"}))
			}
			return m.hook.ExecuteCommandEvent(ctx, queryhooks.CommandEvent{Event: event, SessionID: m.Controller.SessionID(), ToolUseID: "actual-event-use", MatchQuery: "Read", Fields: fields, Call: queryhooks.Context{Scope: scope}}, func(r queryhooks.EventResult) error { results = append(results, r); return nil })
		}); e != nil {
			t.Fatal(e)
		}
		if len(results) != 2 || results[0].Message == nil || results[0].Message.Type() != "progress" || results[1].Outcome != "success" || results[1].Message == nil {
			t.Fatal("actual Hook progress/output", results)
		}
		attachment, _ := results[1].Message.Record.Object("attachment")
		if attachment.String("hookName") != event+":Read" || attachment.String("toolUseID") != "actual-event-use" || attachment.String("type") != "hook_success" || attachment.String("command") != "actual "+event || attachment.Get("durationMs") == nil {
			t.Fatal("actual Hook Factory output", rawMain(attachment))
		}
		if event == "PreToolUse" {
			updated, _ := results[1].Fields.Object("updatedInput")
			if results[1].Fields.String("permissionBehavior") != "allow" || updated.String("file_path") != "changed-by-hook" || results[1].Fields.String("additionalContext") != "actual context" {
				t.Fatal("original PreTool fields lost", rawMain(results[1].Fields))
			}
		} else if results[1].Fields.String("additionalContext") != "actual post context" {
			t.Fatal("original PostTool fields lost")
		}
	}
	raw, e := os.ReadFile(journal)
	if e != nil {
		t.Fatal(e)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatal("physical hook count", string(raw))
	}
	for i, line := range lines {
		var input protocol.JSONObject
		if e = json.Unmarshal([]byte(line), &input); e != nil {
			t.Fatal(e)
		}
		if input.String("session_id") != m.Controller.SessionID() || input.String("tool_use_id") != "actual-event-use" || input.String("hook_event_name") != []string{"PreToolUse", "PostToolUse"}[i] || input.Get("stop_hook_active") != nil {
			t.Fatal("Source event input polluted by Stop fields", line)
		}
	}
	if e = m.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
}
