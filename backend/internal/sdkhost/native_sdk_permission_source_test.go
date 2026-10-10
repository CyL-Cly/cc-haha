package sdkhost

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
)

func TestNativeSDKStdioPermissionOriginalSchemaAndControlChannel(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		{"name": "allow-empty-mobile-input", "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{}, "unknown": "strip"}},
		{"name": "allow-replacement", "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{"actual": "replacement"}, "toolUseID": "original-id", "decisionClassification": "user_permanent"}},
		{"name": "allow-malformed-update-classification-fallback", "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{}, "updatedPermissions": []any{map[string]any{}}, "decisionClassification": "not-valid"}},
		{"name": "allow-empty-updates", "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{}, "updatedPermissions": []any{}}},
		{"name": "allow-real-session-rule-update", "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{}, "updatedPermissions": []any{map[string]any{"type": "addRules", "rules": []any{map[string]any{"toolName": "mcp__sdk-p684__read_sdk", "unknown": "strip"}}, "behavior": "allow", "destination": "session", "unknown": "strip"}}}},
		{"name": "allow-real-session-mode-update", "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{}, "updatedPermissions": []any{map[string]any{"type": "setMode", "mode": "acceptEdits", "destination": "session"}}}},
		{"name": "deny", "response": map[string]any{"behavior": "deny", "message": "actual denial", "toolUseID": "permission-tool-P700", "decisionClassification": "user_reject"}},
		{"name": "deny-interrupt", "response": map[string]any{"behavior": "deny", "message": "actual interrupt", "interrupt": true}},
		{"name": "invalid-allow", "response": map[string]any{"behavior": "allow", "updatedInput": 7}},
		{"name": "invalid-deny", "response": map[string]any{"behavior": "deny", "message": 7}},
		{"name": "peer-error", "peerError": true, "response": map[string]any{}},
		{"name": "abort", "abort": true, "response": map[string]any{}},
		{"name": "EOF", "eof": true, "response": map[string]any{}},
	}
	fixture, _ := filepath.Abs("../../testdata/native-sdk-stdio-permission.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_STDIO_PERMISSION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source SDK stdio permission", err, stderr.String())
	}
	var source struct {
		Rows []struct {
			Name                      string
			Parsed                    json.RawMessage
			Error                     *struct{ Name, Message string }
			Request, Decision, Cancel json.RawMessage
			ParentAborted             bool
			Pending                   int
		}
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatal(err, string(data))
	}
	for i, row := range rows {
		t.Run(row["name"].(string), func(t *testing.T) {
			parsed, err := sdkPermissionOutput(raw(row["response"]))
			if source.Rows[i].Error != nil {
				named, ok := err.(*NativeSDKCallbackError)
				if !ok || named.Name != source.Rows[i].Error.Name || named.Message != source.Rows[i].Error.Message {
					t.Fatal("original schema rejection", err, source.Rows[i].Error)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				var actual, expected any
				_ = json.Unmarshal(raw(parsed), &actual)
				_ = json.Unmarshal(source.Rows[i].Parsed, &expected)
				if !reflect.DeepEqual(actual, expected) {
					t.Fatal("original schema parsing", string(raw(parsed)), string(source.Rows[i].Parsed))
				}
			}
			if source.Rows[i].Pending != 0 {
				t.Fatal("original control request not settled", source.Rows[i].Pending)
			}
		})
	}
}
