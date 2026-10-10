package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDedicatedSDKPermissionsMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	type scenario struct {
		Payloads []json.RawMessage `json:"payloads"`
		Response json.RawMessage   `json:"response,omitempty"`
		Stopped  bool              `json:"stopped,omitempty"`
	}
	ask := func(id, tool, input string) json.RawMessage {
		return raw(protocol.NewJSONObject(field("type", "control_request"), field("request_id", id), field("request", protocol.NewJSONObject(field("subtype", "can_use_tool"), field("tool_name", tool), protocol.JSONField{Name: "input", Value: json.RawMessage(input)}, field("agent_id", "  子 agent  "), field("tool_use_id", "  tool-1 "), field("description", " original description "), field("display_name", " 显示 "), field("permission_suggestions", []any{map[string]any{"type": "addRules", "destination": "userSettings", "unknown": true}, []any{"array", 3}, nil, 4})))))
	}
	cases := []scenario{
		{Payloads: []json.RawMessage{ask("ask", "AskUserQuestion", `{"metadata":{"autoAnswered":true,"keep":4},"questions":[]}`)}, Response: json.RawMessage(`{"requestId":"ask","allowed":true,"updatedInput":{"answers":{"q":"Go"},"metadata":{"autoAnswered":true,"keep":3}},"rule":"always"}`)},
		{Payloads: []json.RawMessage{ask("write", "Write", `[]`)}, Response: json.RawMessage(`{"requestId":"write","allowed":false,"denyMessage":"  用户反馈  "}`)},
		{Payloads: []json.RawMessage{ask("plan", "ExitPlanMode", `{"metadata":[]}`)}, Response: json.RawMessage(`{"requestId":"plan","allowed":false}`)},
		{Payloads: []json.RawMessage{ask("clarify", "AskUserQuestion", `{}`)}, Response: json.RawMessage(`{"requestId":"clarify","allowed":false,"denyMessage":"  clarify  "}`)},
		{Payloads: []json.RawMessage{ask("updates", "Write", `{}`)}, Response: json.RawMessage(`{"requestId":"updates","allowed":true,"permissionUpdates":[{"type":"setMode","mode":"plan","destination":"userSettings"}],"updatedInput":[1,2]}`)},
		{Payloads: []json.RawMessage{ask("cancel", "Read", `{}`), json.RawMessage(`{"type":"control_cancel_request","request_id":"cancel"}`)}},
		{Payloads: []json.RawMessage{ask("resolved", "Read", `{}`), json.RawMessage(`{"type":"control_response","response":{"request_id":"resolved","subtype":"success","response":{}}}`)}},
		{Payloads: []json.RawMessage{ask("reorder", "Read", `{}`), ask("other", "Write", `{}`), json.RawMessage(`{"type":"control_cancel_request","request_id":"reorder"}`), ask("reorder", "Read", `{}`)}},
		{Payloads: []json.RawMessage{ask("late", "Write", `{}`)}, Stopped: true},
		{Payloads: []json.RawMessage{json.RawMessage(`{"type":"control_request","request_id":"","request":{"subtype":"can_use_tool","tool_name":"","input":null,"permission_suggestions":{}}}`)}, Response: json.RawMessage(`{"requestId":"","allowed":true,"rule":"always"}`)},
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-permissions.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_PERMISSIONS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "cases": cases}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	type result struct{ Pending, Sent, After, Forwarded, Frames []json.RawMessage }
	var source struct {
		Rows         []result
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v blocked%v %.500s", e, source.Capabilities.Blocked, out)
	}
	snapshot := func(d *DesktopSession) []json.RawMessage {
		out := []json.RawMessage{}
		state := d.sdkRuntime()
		state.mu.Lock()
		defer state.mu.Unlock()
		for _, id := range state.order {
			f := sdkPermissionFrame(id, state.pending[id])
			m := obj(f)
			m.Delete("type")
			m.Delete("permissionSuggestions")
			out = append(out, raw(m))
		}
		return out
	}
	for i, row := range cases {
		sent := []json.RawMessage{}
		forwarded := []json.RawMessage{}
		frames := []json.RawMessage{}
		d := &DesktopSession{ctx: ctx, projection: NewProjection(), emit: func(data json.RawMessage) { frames = append(frames, data) }, onSDK: func(data json.RawMessage) { forwarded = append(forwarded, data) }}
		unbind := d.BindSDKResponseTransport(func(data json.RawMessage) error { sent = append(sent, data); return nil })
		d.sdkRuntime().stopped = row.Stopped
		for _, payload := range row.Payloads {
			if e = d.AcceptSDKOutput(ctx, payload); e != nil {
				t.Fatal(e)
			}
		}
		pending := snapshot(d)
		initialFrames := append([]json.RawMessage{}, frames...)
		if row.Response != nil {
			if _, e = d.respondSDKPermission(obj(row.Response)); e != nil {
				t.Fatal(e)
			}
		}
		actual := result{pending, sent, snapshot(d), forwarded, initialFrames}
		var a, b any
		_ = json.Unmarshal(raw(actual), &a)
		_ = json.Unmarshal(raw(source.Rows[i]), &b)
		aa, _ := json.Marshal(a)
		bb, _ := json.Marshal(b)
		if !bytes.Equal(aa, bb) {
			t.Errorf("case%d Source%s Go%s", i, bb, aa)
		}
		unbind()
		_ = d.closeSDKTransport(ctx)
	}
}
