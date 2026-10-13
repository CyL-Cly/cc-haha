package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestActualSourceMCPPermissionPromptOutputAbortAndOwnedConsumer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{}
	for _, value := range []any{map[string]any{"behavior": "allow", "updatedInput": map[string]any{}}, map[string]any{"behavior": "allow", "updatedInput": map[string]any{"content": "changed"}, "updatedPermissions": "malformed", "decisionClassification": "unknown", "extra": true}, map[string]any{"behavior": "deny", "message": "explicit denial"}, map[string]any{"behavior": "deny", "message": "interrupt denial", "interrupt": true}, map[string]any{"behavior": "allow"}, map[string]any{"behavior": "unknown"}, nil} {
		text, _ := json.Marshal(value)
		rows = append(rows, map[string]any{"content": []any{map[string]any{"type": "text", "text": string(text)}}})
	}
	rows = append(rows, map[string]any{"content": []any{}}, map[string]any{"content": []any{map[string]any{"type": "image", "text": "{}"}}}, map[string]any{"content": []any{map[string]any{"type": "text", "text": 42}}}, map[string]any{"aborted": true}, map[string]any{"abortDuring": true, "content": []any{map[string]any{"type": "text", "text": `{"behavior":"allow","updatedInput":{}}`}}})
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-mcp-permission.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_MCP_PERMISSION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var expected []protocol.JSONObject
	if err = json.Unmarshal(out, &expected); err != nil {
		t.Fatal(err, string(out))
	}
	owner := cooperative.New()
	defer owner.Close()
	h := &Host{options: Options{Owner: owner}}
	original := protocol.NewJSONObject(protocol.JSONField{Name: "file_path", Value: quote("private.txt")}, protocol.JSONField{Name: "content", Value: quote("original")})
	for i, row := range rows {
		calls := []protocol.JSONObject{}
		modelCtx, abort := context.WithCancel(ctx)
		if row["aborted"] == true {
			abort()
		}
		interrupted := false
		prompt := queryloop.RegisteredTool{Name: "mcp__private__approval", IsMCP: true, CallJSON: func(_ context.Context, payload protocol.JSONObject, _ queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
			calls = append(calls, payload.Clone())
			if row["abortDuring"] == true {
				abort()
			}
			return queryloop.ToolCallResult{Data: raw(row["content"])}, nil
		}, MapResultJSON: func(data, id json.RawMessage) (protocol.JSONObject, error) {
			return protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: data}), nil
		}}
		var decision queryloop.ToolPermission
		err := owner.Run(ctx, func(scope *cooperative.Scope) error {
			var err error
			decision, err = h.callNativeMCPPermissionPrompt(modelCtx, scope, prompt, queryloop.RegisteredTool{Name: "Write"}, &original, queryloop.JSONToolCallContext{ToolUseID: quote("actual-tool-use"), CancelModel: func() { interrupted = true }})
			return err
		})
		abort()
		if !jsonSemanticMCPPermission(raw(calls), expected[i].Get("calls")) {
			t.Error(i, "payload does not match actual Source", string(raw(calls)), string(expected[i].Get("calls")))
		}
		if expected[i].Get("error") != nil {
			var message string
			_ = json.Unmarshal(expected[i].Get("error"), &message)
			if err == nil || sdkPermissionErrorText(err) != message && err.Error() != message {
				t.Error(i, "Source parse/block error", err, message)
			}
			continue
		}
		if err != nil {
			t.Error(i, err)
			continue
		}
		actual := protocol.NewJSONObject(protocol.JSONField{Name: "behavior", Value: quote(decision.Behavior)}, protocol.JSONField{Name: "message", Value: decision.Message}, protocol.JSONField{Name: "decisionReason", Value: decision.DecisionReason})
		reason, _ := object(decision.DecisionReason).Object("toolResult")
		full := reason.Clone()
		full.Assign(actual)
		actual = full
		if decision.UpdatedInput != nil {
			actual.Set("updatedInput", raw(decision.UpdatedInput))
		}
		if !jsonSemanticMCPPermission(raw(actual), expected[i].Get("decision")) {
			t.Error(i, "decision", string(raw(actual)), string(expected[i].Get("decision")))
		}
		if row["content"] != nil && i == 3 && !interrupted {
			t.Error("deny interrupt did not abort model")
		}
	}
}
func jsonSemanticMCPPermission(a, b json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}
