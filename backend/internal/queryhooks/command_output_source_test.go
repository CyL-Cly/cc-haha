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

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeHookOutputDeclarationsAndValidationMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	home := t.TempDir()
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
	values := []json.RawMessage{}
	for _, raw := range []string{`{}`, `null`, `[]`, `{"continue":false,"decision":"block","reason":"Actual reason","future":true}`, `{"continue":"false"}`, `{"suppressOutput":true,"systemMessage":"private\ud800","permissionDecision":"deny"}`, `{"async":true,"asyncTimeout":-1,"decision":"block"}`, `{"async":false,"decision":"approve"}`, `{"decision":"unknown"}`, `{"reason":7}`, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","updatedInput":{"unknown":true}}}`, `{"hookSpecificOutput":{"hookEventName":"SubagentStop","decision":"block"}}`, `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":"x"}}}`, `{"hookSpecificOutput":{"hookEventName":"CwdChanged","watchPaths":["/private"]}}`} {
		values = append(values, json.RawMessage(raw))
	}
	run := func(input any) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
		raw, _ := json.Marshal(input)
		cmd.Stdin = bytes.NewReader(raw)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, e := cmd.Output()
		if e != nil {
			t.Fatal(e, stderr.String())
		}
		return output
	}
	schema := run(map[string]any{"homeDir": home, "schema": true})
	var a, b any
	_ = json.Unmarshal(schema, &a)
	_ = json.Unmarshal(hookOutputDeclaration, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("original output declaration changed")
	}
	var source struct{ Rows []json.RawMessage }
	if e := json.Unmarshal(run(map[string]any{"homeDir": home, "values": values}), &source); e != nil || len(source.Rows) != len(values) {
		t.Fatal(e)
	}
	native, e := HookOutputSchema()
	if e != nil {
		t.Fatal(e)
	}
	for i, value := range values {
		parsed, issues := pluginschema.Validate(native, value)
		actual := map[string]any{"value": json.RawMessage(parsed)}
		if len(issues) > 0 {
			actual = map[string]any{"issues": issues}
		}
		raw, _ := json.Marshal(actual)
		var a, b any
		_ = json.Unmarshal(raw, &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case%d Go%s Source%s", i, raw, source.Rows[i])
		}
	}
	pipeline := []map[string]any{}
	for _, stdout := range []string{`{}`, `{"decision":"block","reason":"private\ud800"}`, `{"continue":false,"decision":"block","reason":"twice","stopReason":"reason\ud800","systemMessage":"system\ud800"}`, `{"continue":false}`, `{"continue":"wrong"}`, `{"reason":7}`, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny"}}`, `{broken JSON`, `plain text`, `{"unknown":"stripped"}`} {
		pipeline = append(pipeline, map[string]any{"stdout": stdout, "stderr": "raw stderr", "code": 0, "command": "actual-display", "event": "SubagentStop"})
	}
	var original struct{ Rows []protocol.JSONObject }
	if e = json.Unmarshal(run(map[string]any{"homeDir": home, "pipeline": pipeline}), &original); e != nil || len(original.Rows) != len(pipeline) {
		t.Fatal(e)
	}
	for i, row := range pipeline {
		got, failure := CommandStopDecision("actual-display", "SubagentStop", CommandOutput{Stdout: row["stdout"].(string), Stderr: "raw stderr", DurationMillis: 42})
		parsed, _ := original.Rows[i].Object("parsed")
		processed, _ := original.Rows[i].Object("processed")
		blocked, _ := processed.Object("blockingError")
		var errorText any
		if failure != nil {
			errorText = failure.Error()
		}
		actual := protocol.NewJSONObject(protocol.JSONField{Name: "validation", Value: commandJSON(got.ValidationError)}, protocol.JSONField{Name: "prevent", Value: commandJSON(got.PreventContinuation)}, protocol.JSONField{Name: "block", Value: got.blockingJSON}, protocol.JSONField{Name: "reason", Value: got.stopReasonJSON}, protocol.JSONField{Name: "system", Value: got.systemJSON}, protocol.JSONField{Name: "failure", Value: commandJSON(errorText)})
		prevent := processed.Get("preventContinuation")
		if prevent == nil {
			prevent = commandJSON(false)
		}
		validation := parsed.Get("validationError")
		if validation == nil {
			validation = commandJSON("")
		}
		expected := protocol.NewJSONObject(protocol.JSONField{Name: "validation", Value: validation}, protocol.JSONField{Name: "prevent", Value: prevent}, protocol.JSONField{Name: "block", Value: blocked.Get("blockingError")}, protocol.JSONField{Name: "reason", Value: processed.Get("stopReason")}, protocol.JSONField{Name: "system", Value: processed.Get("systemMessage")}, protocol.JSONField{Name: "failure", Value: original.Rows[i].Get("failure")})
		a, _ := actual.MarshalJSON()
		b, _ := expected.MarshalJSON()
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(a), protocol.StringifyValidatedJavaScriptJSON(b)) {
			t.Errorf("pipeline%d Go%s Source%s", i, a, b)
		}
	}
}
