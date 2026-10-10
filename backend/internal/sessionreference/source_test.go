package sessionreference

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncollaboration"
)

func TestReferenceContextSequentialLookupDedupAndUTF16MatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []json.RawMessage{[]byte(`{"content":"request"}`)}
	for _, references := range []any{nil, false, map[string]any{}, []any{}, []any{nil}, []any{map[string]any{"sessionId": ""}}, []any{map[string]any{"sessionId": "\ufeff "}}, []any{map[string]any{"sessionId": strings.Repeat("😀", 101)}}, []any{map[string]any{"sessionId": "first"}, map[string]any{"sessionId": "first"}, map[string]any{"sessionId": "second"}}, []any{map[string]any{"sessionId": "<>&\""}, map[string]any{"sessionId": "中文😀"}}} {
		row, _ := json.Marshal(map[string]any{"content": "request", "references": references})
		rows = append(rows, row)
	}
	rows = append(rows, []byte(`{"content":"\ud800 request","references":[{"sessionId":"\ud800"},{"sessionId":"\ufffd"},{"sessionId":"\ud800"}]}`), []byte(`{"content":"request","references":[{"sessionId":"first"},{"sessionId":"missing"},{"sessionId":"never"}],"missing":["missing"]}`))
	tooMany := []any{}
	for i := 0; i < 21; i++ {
		tooMany = append(tooMany, map[string]any{"sessionId": "valid"})
	}
	data, _ := json.Marshal(map[string]any{"content": "request", "references": tooMany})
	rows = append(rows, data)
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-session-bridge.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SESSION_BRIDGE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "configurations": []any{}, "credentials": []any{}, "responses": []any{}, "references": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source reference %v %.2000s", e, stderr.String())
	}
	var source struct {
		References   []json.RawMessage
		Capabilities struct{ Blocked []any }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.References) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal("Source shape", e, string(output))
	}
	for index, raw := range rows {
		var row protocol.JSONObject
		_ = row.UnmarshalJSON(raw)
		seen := []json.RawMessage{}
		var missing []json.RawMessage
		_ = json.Unmarshal(row.Get("missing"), &missing)
		content, e := Resolve(ctx, row.Get("content"), row.Get("references"), func(_ context.Context, id json.RawMessage) (bool, error) {
			seen = append(seen, id)
			for _, m := range missing {
				if string(m) == string(id) {
					return false, nil
				}
			}
			return true, nil
		})
		result := protocol.NewJSONObject(protocol.JSONField{Name: "seen", Value: refRaw(seen)})
		if e != nil {
			failure := e.(*sessioncollaboration.APIError)
			result.Set("error", refRaw(map[string]any{"code": failure.Code, "message": failure.Message, "status": failure.Status}))
		} else {
			result.Set("content", content)
		}
		if !reflect.DeepEqual(refTree(t, refRaw(result)), refTree(t, source.References[index])) {
			t.Errorf("reference%d Go%s Source%s", index, refRaw(result), source.References[index])
		}
	}
}
func refRaw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func refTree(t *testing.T, data json.RawMessage) any {
	t.Helper()
	if len(data) > 0 && data[0] == '{' {
		var o protocol.JSONObject
		_ = o.UnmarshalJSON(data)
		out := map[string]any{}
		for _, f := range o.OwnProperties() {
			out[string(f.NameJSON)] = refTree(t, f.Value)
		}
		return out
	}
	if len(data) > 0 && data[0] == '[' {
		var a []json.RawMessage
		_ = json.Unmarshal(data, &a)
		out := []any{}
		for _, v := range a {
			out = append(out, refTree(t, v))
		}
		return out
	}
	value, e := protocol.ParseAndStringifyJavaScriptJSON(data)
	if e != nil {
		t.Fatal(e)
	}
	return string(value)
}
