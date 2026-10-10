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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeSDKMCPMessageActualSourceRequestsErrorsAndEOF(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Join(home, ".claude"), 0700); e != nil {
		t.Fatal(e)
	}
	fixture, _ := filepath.Abs("../../testdata/native-sdk-mcp-message.fixture.ts")
	preload, _ := filepath.Abs("../../../preload")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, finish := context.WithTimeout(context.Background(), 10*time.Second)
	defer finish()
	var message protocol.JSONObject
	_ = message.UnmarshalJSON([]byte(`{"jsonrpc":"2.0","id":0,"method":"tools/list"}`))
	rows := []map[string]any{{"response": map[string]any{"mcp_response": map[string]any{"jsonrpc": "2.0", "id": 0, "result": map[string]any{"tools": []any{}}}}}, {"error": true}, {"eof": true}, {"response": map[string]any{}}, {"response": map[string]any{"mcp_response": nil}}, {"response": map[string]any{"mcp_response": 7}}}
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", "--preload", preload, fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_MCP_MESSAGE_FIXTURE=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(raw(map[string]any{"message": message, "rows": rows}))
	var errors bytes.Buffer
	command.Stderr = &errors
	data, e := command.Output()
	if e != nil {
		t.Fatal("actual Source message", e, errors.String())
	}
	var source []struct {
		Request protocol.JSONObject
		Value   json.RawMessage
		Error   string
	}
	if e = json.Unmarshal(data, &source); e != nil || len(source) != len(rows) {
		t.Fatal(e, string(data))
	}
	for i, row := range rows {
		owner := cooperative.New()
		state, e := NewNativeSDKMCPState(owner)
		if e != nil {
			t.Fatal(e)
		}
		var frame protocol.JSONObject
		var future *OwnerFuture[json.RawMessage]
		e = owner.Run(ctx, func(scope *cooperative.Scope) error {
			if e := state.bind(scope, func(_ *cooperative.Scope, event protocol.JSONObject) error { frame = event.Clone(); return nil }, func() string { return "actual-mcp-request" }); e != nil {
				return e
			}
			var e error
			future, e = state.Send(scope, "peer", message)
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		e = owner.Run(ctx, func(scope *cooperative.Scope) error {
			if row["eof"] == true {
				return state.close(scope)
			}
			response := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("success")}, protocol.JSONField{Name: "request_id", Value: quote("actual-mcp-request")}, protocol.JSONField{Name: "response", Value: raw(row["response"])})
			if row["error"] == true {
				response.Set("subtype", quote("error"))
				response.Set("error", quote("actual peer error"))
			}
			_, e := state.response(scope, protocol.NewJSONObject(protocol.JSONField{Name: "response", Value: raw(response)}))
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		value, e := future.Wait(ctx)
		errText := ""
		if e != nil {
			errText = e.Error()
		}
		if errText != source[i].Error {
			t.Fatal("Source rejection", i, errText, source[i].Error)
		}
		if e == nil {
			if !bytes.Equal(bytes.TrimSpace(value), bytes.TrimSpace(source[i].Value)) {
				t.Fatal("Source raw any preserves undefined/null/scalar", string(value), string(source[i].Value))
			}
			var a, b any
			_ = json.Unmarshal(value, &a)
			_ = json.Unmarshal(source[i].Value, &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatal("Source MCP value", a, b)
			}
		}
		got, _ := frame.Object("request")
		want, _ := source[i].Request.Object("request")
		var a, b any
		_ = json.Unmarshal(raw(got), &a)
		_ = json.Unmarshal(raw(want), &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatal("original MCP control frame", a, b)
		}
		if len(state.pending) != 0 {
			t.Fatal("MCP control future leaked")
		}
		owner.Close()
	}
}
