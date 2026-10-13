package remoteio

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
)

func runSource(t *testing.T, input any) protocol.JSONObject {
	return runSourceFixture(t, "native-remoteio-contract.fixture.ts", input)
}
func runSourceFixture(t *testing.T, fixtureName string, input any) protocol.JSONObject {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	allocation, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(allocation, "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../testdata/" + fixtureName)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "TMPDIR="+home, "CLAUDE_CONFIG_DIR="+config, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT="+allocation, "CC_HAHA_REMOTEIO_FIXTURE=1", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "http_proxy=", "https_proxy=", "all_proxy=", "NO_PROXY=*")
	cmd.Stdin = strings.NewReader(string(raw(input)))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatal(err, string(data), stderr.String())
	}
	if stderr.Len() > 0 {
		t.Logf("actual Source native diagnostic: %s", stderr.String())
	}
	var result protocol.JSONObject
	if err := result.UnmarshalJSON(data); err != nil {
		t.Fatal(err, string(data))
	}
	if blocked := object(result.Get("capabilities")).Get("blocked"); string(blocked) != "[]" {
		t.Fatal("Source accessed outside fixture", string(blocked))
	}
	return result
}

func TestActualSourceTransportSelectionFrameParserAndStreamSnapshots(t *testing.T) {
	type selection struct {
		URL string            `json:"url"`
		Env map[string]string `json:"env"`
	}
	selections := []selection{
		{"ws://example.invalid/ws/source", nil},
		{"wss://example.invalid/ws/source?one=1", map[string]string{"CLAUDE_CODE_POST_FOR_SESSION_INGRESS_V2": "1"}},
		{"ws://example.invalid/a/ws/source/events?token=opaque", map[string]string{"CLAUDE_CODE_POST_FOR_SESSION_INGRESS_V2": "true"}},
		{"https://example.invalid/v1/code/sessions/source/?q=1", map[string]string{"CLAUDE_CODE_USE_CCR_V2": "1", "CLAUDE_CODE_POST_FOR_SESSION_INGRESS_V2": "1"}},
		{"http://example.invalid/v1/code/sessions/source", map[string]string{"CLAUDE_CODE_USE_CCR_V2": "1"}},
		{"wss://example.invalid/session/source", map[string]string{"CLAUDE_CODE_USE_CCR_V2": "1"}},
		{"https://example.invalid/session/source", nil},
		{"file:///private/tmp/source", nil},
	}
	frames := []string{"", ":keepalive\n\n", "event: client_event\nid: 12\ndata: {\"payload\":{\"type\":\"keep_alive\"}}\n\npartial", "data: one\ndata: two\n\n", "event:other\nid:7\n\n", ":comment\nevent: client_event\ndata: x\n\n", strings.Repeat("界", MaxSSEFrameBytes/3+1), strings.Repeat("x", MaxSSEFrameBytes) + "\n\n"}
	stream := []protocol.JSONObject{
		object([]byte(`{"messages":[{"type":"stream_event","uuid":"s","session_id":"a","parent_tool_use_id":null,"event":{"type":"message_start","message":{"id":"msg-a"}}},{"type":"stream_event","uuid":"d1","session_id":"a","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}},{"type":"stream_event","uuid":"d2","session_id":"a","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}}]}`)),
		object([]byte(`{"messages":[{"type":"stream_event","uuid":"d3","session_id":"a","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"!"}}},{"type":"stream_event","uuid":"tool","session_id":"b","parent_tool_use_id":"tool","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"peer"}}}]}`)),
		object([]byte(`{"clear":{"session_id":"a","parent_tool_use_id":null,"message":{"id":"msg-a"}},"messages":[{"type":"stream_event","uuid":"late","session_id":"a","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"after clear"}}}]}`)),
		object([]byte(`{"messages":[{"type":"stream_event","uuid":"unicode-start","session_id":"a","parent_tool_use_id":null,"event":{"type":"message_start","message":{"id":"\ud800"}}},{"type":"stream_event","uuid":"unicode-1","session_id":"a","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"\ud800\ud83d"}}}]}`)),
		object([]byte(`{"messages":[{"type":"stream_event","uuid":"unicode-2","session_id":"a","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"\ude00\u0000"}}}]}`)),
	}
	want := runSource(t, map[string]any{"selections": selections, "frames": frames, "stream": stream})
	selected := []map[string]any{}
	for _, row := range selections {
		e, err := SelectEndpoints(row.URL, row.Env)
		if err != nil {
			selected = append(selected, map[string]any{"error": err.Error()})
			continue
		}
		kind := map[Kind]string{WebSocket: "WebSocketTransport", Hybrid: "HybridTransport", CCR: "SSETransport"}[e.Kind]
		v := map[string]any{"kind": kind, "stream": e.Stream}
		if e.Events != "" {
			v["events"] = e.Events
		}
		if e.Session != "" {
			v["session"] = e.Session
		}
		selected = append(selected, v)
	}
	parsed := []map[string]any{}
	for _, buffer := range frames {
		rows, remaining, err := ParseSSEFrames(buffer)
		if err != nil {
			parsed = append(parsed, map[string]any{"error": err.Error()})
			continue
		}
		list := []map[string]any{}
		for _, frame := range rows {
			v := map[string]any{}
			if frame.Event != "" {
				v["event"] = frame.Event
			}
			if frame.ID != "" {
				v["id"] = frame.ID
			}
			if frame.Data != "" {
				v["data"] = frame.Data
			}
			list = append(list, v)
		}
		parsed = append(parsed, map[string]any{"frames": list, "remaining": remaining})
	}
	accumulator := newStreamAccumulator()
	snapshots := [][]protocol.JSONObject{}
	for _, row := range stream {
		if clear := object(row.Get("clear")); clear.IsObject() {
			accumulator.clear(clear)
		}
		var list []protocol.JSONObject
		_ = json.Unmarshal(row.Get("messages"), &list)
		snapshots = append(snapshots, accumulator.accumulate(list))
	}
	for name, value := range map[string]any{"selections": selected, "frames": parsed, "stream": snapshots} {
		var a, b any
		_ = json.Unmarshal(raw(value), &a)
		_ = json.Unmarshal(want.Get(name), &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("actual Source %s%s Go%s", name, want.Get(name), raw(value))
		}
	}
	units := [][][]uint16{}
	for _, rows := range snapshots {
		list := [][]uint16{}
		for _, row := range rows {
			var value []uint16
			text := object(object(row.Get("event")).Get("delta")).Get("text")
			if len(text) > 0 {
				value, _ = protocol.DecodeJavaScriptJSONString(text)
			}
			list = append(list, value)
		}
		units = append(units, list)
	}
	var sourceUnits any
	_ = json.Unmarshal(want.Get("streamUnits"), &sourceUnits)
	var goUnits any
	_ = json.Unmarshal(raw(units), &goUnits)
	if !reflect.DeepEqual(sourceUnits, goUnits) {
		t.Errorf("actual Source UTF16 snapshot units%s Go%s", want.Get("streamUnits"), raw(units))
	}
}
