package queryhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAllHookJSONOutputBusinessFieldsAndMessagesMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	rows := []protocol.JSONObject{}
	add := func(event, output string) {
		rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "event", Value: commandJSON(event)}, protocol.JSONField{Name: "command", Value: commandJSON("actual display")}, protocol.JSONField{Name: "stdout", Value: commandJSON(output)}, protocol.JSONField{Name: "stderr", Value: commandJSON("actual error")}, protocol.JSONField{Name: "code", Value: []byte("0")}))
	}
	for _, event := range []string{"Stop", "SubagentStop", "PreToolUse", "PostToolUse", "SessionStart", "UserPromptSubmit"} {
		add(event, `{"decision":"approve","reason":"original reason","systemMessage":"system\ud800","continue":false,"stopReason":"reason\ud800"}`)
		add(event, `{"decision":"block","reason":""}`)
	}
	for _, decision := range []string{"allow", "ask", "deny"} {
		for _, reason := range []string{"", `"permissionDecisionReason":"specific\ud800",`} {
			add("PreToolUse", `{"decision":"block","reason":"general","hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"`+decision+`",`+reason+`"updatedInput":{"raw":"\ud800"},"additionalContext":"extra"}}`)
		}
	}
	for _, event := range []string{"UserPromptSubmit", "SessionStart", "Setup", "SubagentStart", "PostToolUse", "PostToolUseFailure"} {
		add(event, `{"hookSpecificOutput":{"hookEventName":"`+event+`","additionalContext":"context\ud800"}}`)
	}
	add("SessionStart", `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"","initialUserMessage":"first"}}`)
	add("PostToolUse", `{"hookSpecificOutput":{"hookEventName":"PostToolUse","updatedMCPToolOutput":{"content":"modified"}}}`)
	add("PermissionDenied", `{"hookSpecificOutput":{"hookEventName":"PermissionDenied","retry":true}}`)
	add("PermissionRequest", `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow","updatedInput":{"data":"updated"},"updatedPermissions":[]}}}`)
	add("PermissionRequest", `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":"Denied","interrupt":true}}}`)
	for _, event := range []string{"Elicitation", "ElicitationResult"} {
		for _, action := range []string{"accept", "decline", "cancel"} {
			add(event, `{"reason":"original","hookSpecificOutput":{"hookEventName":"`+event+`","action":"`+action+`","content":{"value":"raw\ud800"}}}`)
		}
	}
	add("Stop", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny"}}`)
	home := t.TempDir()
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(commandJSON(map[string]any{"homeDir": home, "pipeline": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct{ Rows []protocol.JSONObject }
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(rows) {
		t.Fatal(e, string(out))
	}
	schema, e := HookOutputSchema()
	if e != nil {
		t.Fatal(e)
	}
	for i, row := range rows {
		parsed, issues := pluginschema.Validate(schema, []byte(row.String("stdout")))
		if len(issues) > 0 {
			t.Fatal("declared supported schema unexpectedly invalid", i, issues)
		}
		var value protocol.JSONObject
		_ = value.UnmarshalJSON(parsed)
		stdout, stderr, code, duration := row.String("stdout"), row.String("stderr"), 0, float64(42)
		got, failure := InterpretJSONOutput(value, JSONOutputContext{Command: row.String("command"), HookName: row.String("event"), Event: row.String("event"), ExpectedEvent: row.String("event"), ToolUseID: "original-output-id", Stdout: &stdout, Stderr: &stderr, ExitCode: &code, DurationMillis: &duration})
		if failure != nil {
			if source.Rows[i].String("failure") != failure.Error() {
				t.Errorf("error%d Go%v Source%s", i, failure, source.Rows[i].Get("failure"))
			}
			continue
		}
		actual := got.Fields.Clone()
		actual.Set("message", commandJSON(protocol.NewJSONObject(protocol.JSONField{Name: "attachment", Value: commandJSON(got.Attachment)})))
		raw, _ := actual.MarshalJSON()
		want := source.Rows[i].Get("processed")
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(raw), protocol.StringifyValidatedJavaScriptJSON(want)) {
			t.Errorf("case%d Go%s Source%s", i, raw, want)
		}
	}
}
