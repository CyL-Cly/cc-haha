package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestActualSourceTeammateTurnFailureResultAndIdleCaps(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	msg := func(kind string, content any, extra map[string]any) protocol.JSONObject {
		fields := map[string]any{"type": kind, "message": map[string]any{"role": kind, "content": content}}
		for key, value := range extra {
			fields[key] = value
		}
		return objMain(rawMain(fields))
	}
	prompt := msg("user", "actual prompt", nil)
	answer := func(text string) protocol.JSONObject {
		return msg("assistant", []map[string]string{{"type": "text", "text": text}}, nil)
	}
	tool := msg("assistant", []any{map[string]string{"type": "text", "text": "REPORT_ONCE"}, map[string]any{"type": "tool_use", "id": "send", "name": "SendMessage", "input": map[string]string{"to": "team-lead"}}}, nil)
	result := func(success bool) protocol.JSONObject {
		return msg("user", []map[string]any{{"type": "tool_result", "tool_use_id": "send", "content": []map[string]string{{"type": "text", "text": string(rawMain(map[string]bool{"success": success}))}}}}, nil)
	}
	histories := [][]protocol.JSONObject{{prompt, answer("  actual result\n  ")}, {prompt, tool, result(true)}, {prompt, tool, result(false)}, {prompt, tool, result(true), answer("new text after sent")}, {prompt, answer("previous"), msg("user", "next prompt", nil)}}
	for _, text := range []string{"API Error: 529 Overloaded", "API Error: 401 Unauthorized", "API Error: Request was aborted.", "[Request interrupted by user]", "The user doesn't want to take this action right now. STOP what you are doing and wait for the user to tell you how to proceed.", "\n API Error: stream_truncated\nsecond line", strings.Repeat("界", 205)} {
		histories = append(histories, []protocol.JSONObject{prompt, msg("assistant", []map[string]string{{"type": "text", "text": text}}, map[string]any{"isApiErrorMessage": true})})
	}
	for _, details := range []any{nil, 42, map[string]bool{"detail": true}, "API Error:503 via details"} {
		histories = append(histories, []protocol.JSONObject{prompt, msg("assistant", []any{}, map[string]any{"isApiErrorMessage": true, "errorDetails": details})})
	}
	histories = append(histories, []protocol.JSONObject{prompt, msg("assistant", []map[string]any{{"type": "text", "text": nil}, {"type": "text", "text": 42}, {"type": "text", "text": map[string]bool{"unknown": true}}}, nil)})
	caps := []map[string]any{{"result": " \x00done\x7f\ufeff ", "failure": "\n first error\u2028 \nsecond", "reachable": true}, {"result": strings.Repeat("a", 3999) + "😀tail", "failure": strings.Repeat("x", 199) + "😀tail", "reachable": false}, {"result": "  ", "failure": "\x00", "reachable": true}}
	actualRows := []any{}
	for _, history := range histories {
		result, ok, err := teamspawn.TeammateTurnResult(history)
		if err != nil {
			t.Fatal(err)
		}
		failure, err := teamspawn.ClassifyTeammateTurnFailure(history)
		if err != nil {
			t.Fatal(err)
		}
		var projected any
		if failure != nil {
			projected = map[string]any{"reason": failure.Reason, "isTransient": failure.Transient}
		}
		var output any
		if ok {
			output = result
		}
		actualRows = append(actualRows, map[string]any{"result": output, "failure": projected})
	}
	actualCaps := []map[string]any{}
	for _, row := range caps {
		output := map[string]any{}
		if text := nativeCapIdleResult(row["result"].(string), row["reachable"].(bool)); text != "" {
			output["result"] = text
		}
		if text := nativeCapIdleFailure(row["failure"].(string)); text != "" {
			output["failure"] = text
		}
		actualCaps = append(actualCaps, output)
	}
	unicodeInputs := []map[string]any{}
	unicodeActual := []map[string]any{}
	asUnits := func(raw json.RawMessage) any {
		if raw == nil || string(raw) == `""` {
			return nil
		}
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		if err != nil {
			t.Fatal(err)
		}
		values := make([]int, len(units))
		for i, unit := range units {
			values[i] = int(unit)
		}
		return values
	}
	texts := [][]uint16{{' ', 0xd800, 'x', 0xdc00, ' '}, append(utf16.Encode([]rune(strings.Repeat("a", 3999))), 0xd800, 'x'), append(utf16.Encode([]rune(strings.Repeat("a", 199))), 0xd800, 'x'), {0, '\n', 'f', 0xd800}}
	for _, units := range texts {
		text := json.RawMessage(protocol.QuoteJavaScriptUTF16(units))
		for _, isAPI := range []bool{false, true} {
			history := []protocol.JSONObject{prompt, msg("assistant", []map[string]any{{"type": "text", "text": text}}, map[string]any{"isApiErrorMessage": isAPI})}
			unicodeInputs = append(unicodeInputs, map[string]any{"history": history, "result": text, "failure": text, "reachable": true})
			result, found, err := teamspawn.TeammateTurnResultRawJSON(history)
			if err != nil {
				t.Fatal(err)
			}
			if !found {
				result = nil
			}
			failure, err := teamspawn.ClassifyTeammateTurnFailure(history)
			if err != nil {
				t.Fatal(err)
			}
			var reason json.RawMessage
			if failure != nil {
				reason = failure.ReasonRaw
			}
			cappedResult, err := nativeCapIdleResultRawJSON(text, true)
			if err != nil {
				t.Fatal(err)
			}
			cappedFailure, err := nativeCapIdleFailureRawJSON(text)
			if err != nil {
				t.Fatal(err)
			}
			unicodeActual = append(unicodeActual, map[string]any{"result": asUnits(result), "reason": asUnits(reason), "cappedResult": asUnits(cappedResult), "cappedFailure": asUnits(cappedFailure)})
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-inprocess-teammate-continuation.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INPROCESS_CONTINUATION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "histories": histories, "caps": caps, "unicode": unicodeInputs}))
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	var expected struct {
		Rows, Caps, Unicode any
		Capabilities        struct{ Blocked []string }
	}
	if err = json.Unmarshal(data, &expected); err != nil || len(expected.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(data))
	}
	var rows, capped, unicodeRows any
	_ = json.Unmarshal(rawMain(actualRows), &rows)
	_ = json.Unmarshal(rawMain(actualCaps), &capped)
	_ = json.Unmarshal(rawMain(unicodeActual), &unicodeRows)
	if !reflect.DeepEqual(rows, expected.Rows) || !reflect.DeepEqual(capped, expected.Caps) || !reflect.DeepEqual(unicodeRows, expected.Unicode) {
		t.Fatalf("Go%s caps%s Source%s", rawMain(actualRows), rawMain(actualCaps), data)
	}
}
