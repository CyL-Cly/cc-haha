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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeSDKEventRegistrationActualSourceStructuredCallbackConsumer(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	rows := []map[string]any{{"id": "pre", "event": "PreToolUse", "fields": map[string]any{"tool_name": "Read", "tool_input": map[string]any{"file_path": "before"}, "tool_use_id": "actual-read-id"}, "output": map[string]any{"systemMessage": "actual SDK pre", "hookSpecificOutput": map[string]any{"hookEventName": "PreToolUse", "permissionDecision": "allow", "updatedInput": map[string]any{"file_path": "after"}, "additionalContext": "actual SDK context"}}}, {"id": "post", "event": "PostToolUse", "fields": map[string]any{"tool_name": "Read", "tool_input": map[string]any{}, "tool_response": map[string]any{"text": "physical"}, "tool_use_id": "actual-read-id"}, "output": map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PostToolUse", "additionalContext": "after actual Read"}}}, {"id": "stop", "event": "Stop", "fields": map[string]any{"stop_hook_active": false}, "output": map[string]any{"decision": "block", "reason": "actual SDK continue"}}, {"id": "async", "event": "PreToolUse", "fields": map[string]any{"tool_name": "Read"}, "output": map[string]any{"async": true}}}
	fixture, _ := filepath.Abs("../../testdata/native-sdk-callback-event.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, finish := context.WithTimeout(context.Background(), 15*time.Second)
	defer finish()
	c := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	c.Dir = home
	c.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_CALLBACK_EVENT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	c.Stdin = bytes.NewReader(commandJSON(map[string]any{"sid": "sdk-event-sid", "toolUseID": "actual-read-id", "rows": rows}))
	var stderr bytes.Buffer
	c.Stderr = &stderr
	data, e := c.Output()
	if e != nil {
		t.Fatal("actual Source callback execution", e, stderr.String())
	}
	var source []protocol.JSONObject
	if e = json.Unmarshal(data, &source); e != nil || len(source) != len(rows) {
		t.Fatal(e, string(data))
	}
	for i, row := range rows {
		if source[i].Get("error") != nil {
			t.Fatal("actual Source callback error", i, string(commandJSON(source[i])))
		}
		owner := cooperative.New()
		factory, err := messages.NewFactory(func() string { return "sdk-native-event" }, func() time.Time { return time.Unix(0, 0) })
		if err != nil {
			t.Fatal(err)
		}
		s := &Service{options: Options{Profile: ResolvedSettingsProfile, Commands: &CommandBindings{Owner: owner, Factory: factory}, Callbacks: &CallbackBindings{Owner: owner, Store: &CallbackStore{events: map[string][]protocol.JSONObject{}}, Run: func(_ context.Context, call Context, hook, input protocol.JSONObject) (protocol.JSONObject, error) {
			request, _ := source[i].Object("request")
			packet, _ := request.Object("request")
			if call.HookID != packet.String("tool_use_id") {
				t.Error("Source original tool ID argument", call.HookID, packet.String("tool_use_id"))
			}
			var result protocol.JSONObject
			err := result.UnmarshalJSON(commandJSON(row["output"]))
			return result, err
		}}}}
		var result EventResult
		e = owner.Run(ctx, func(scope *cooperative.Scope) error {
			hooks := protocol.NewJSONObject(protocol.JSONField{Name: row["event"].(string), Value: commandJSON([]any{map[string]any{"matcher": "Read", "hookCallbackIds": []string{row["id"].(string)}}})})
			if e := s.RegisterSDKWorktreeCallbacks(scope, hooks); e != nil {
				return e
			}
			returned, e := s.runNativeSDKEvent(ctx, Context{Scope: scope, HookEvent: row["event"].(string)}, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("callback")}), protocol.NewJSONObject(), "actual-read-id")
			result = returned
			return e
		})
		owner.Close()
		if e != nil {
			t.Fatal("SDK registration rejected original callback consumer", i, e)
		}
		want, _ := source[i].Object("result")
		fields, _ := want.Object("fields")
		gotFields := result.Fields.Clone()
		gotFields.Set("outcome", commandJSON(result.Outcome))
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(commandJSON(gotFields)), protocol.StringifyValidatedJavaScriptJSON(commandJSON(fields))) {
			var a, b map[string]json.RawMessage
			_ = json.Unmarshal(commandJSON(gotFields), &a)
			_ = json.Unmarshal(commandJSON(fields), &b)
			if !sameSDKEventValue(a, b) {
				t.Fatal("Source callback interpreted fields", i, string(commandJSON(gotFields)), string(commandJSON(fields)))
			}
		}
		var attachment protocol.JSONObject
		if result.Message != nil {
			attachment, _ = result.Message.Record.Object("attachment")
		}
		if !sameSDKEventValue(attachment, objectForSDKEvent(want.Get("attachment"))) {
			t.Fatal("Source callback attachment has no synthetic stdout/code", i, string(commandJSON(attachment)), string(want.Get("attachment")))
		}
	}
}

func objectForSDKEvent(data json.RawMessage) protocol.JSONObject {
	var result protocol.JSONObject
	_ = result.UnmarshalJSON(data)
	return result
}
func sameSDKEventValue(a, b any) bool {
	var x, y any
	_ = json.Unmarshal(commandJSON(a), &x)
	_ = json.Unmarshal(commandJSON(b), &y)
	return reflect.DeepEqual(x, y)
}
