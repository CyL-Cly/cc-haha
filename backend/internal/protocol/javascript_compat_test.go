package protocol

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// These malformed proxy values are deliberately accepted by the reference
// raw Stream. Testing decoded assistant content, rather than the coercion
// helper, freezes the user-visible behavior observed in the TS/Go replay lane.
func TestAnthropicProxyTextValuesKeepJavaScriptConcatenation(t *testing.T) {
	for _, test := range []struct {
		name     string
		raw      json.RawMessage
		expected string
	}{
		{"missing", nil, "undefined"},
		{"null", json.RawMessage(`null`), "null"},
		{"true", json.RawMessage(`true`), "true"},
		{"false", json.RawMessage(`false`), "false"},
		{"integer", json.RawMessage(`3`), "3"},
		{"negative zero", json.RawMessage(`-0`), "0"},
		{"JS integer precision", json.RawMessage(`9007199254740993`), "9007199254740992"},
		{"fixed large number", json.RawMessage(`1e20`), "100000000000000000000"},
		{"exponential large number", json.RawMessage(`1e21`), "1e+21"},
		{"fixed small number", json.RawMessage(`1e-6`), "0.000001"},
		{"exponential small number", json.RawMessage(`1e-7`), "1e-7"},
		{"overflow", json.RawMessage(`1e400`), "Infinity"},
		{"negative overflow", json.RawMessage(`-1e400`), "-Infinity"},
		{"overflow array", json.RawMessage(`[1e400,null,-1e400]`), "Infinity,,-Infinity"},
		{"underflow", json.RawMessage(`1e-400`), "0"},
		{"unicode string", json.RawMessage(`"中文🙂"`), "中文🙂"},
		{"object", json.RawMessage(`{"proxy":true}`), "[object Object]"},
		{"array", json.RawMessage(`["a",null,3,[null,"b"],{"x":true}]`), "a,,3,,b,[object Object]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			delta := map[string]any{"type": "text_delta"}
			if test.raw != nil {
				delta["text"] = test.raw
			}
			input := messageStart() + blockStart(0, map[string]any{"type": "text"}) + blockDelta(0, delta) + blockStop(0) + terminal("end_turn", true)
			message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), nil)
			if err != nil || message.Text() != test.expected {
				t.Fatalf("reference += behavior changed: %#v, %v; want %q", message, err, test.expected)
			}
		})
	}
}

func TestAnthropicProxyThinkingAndSignatureKeepAssignmentSemantics(t *testing.T) {
	for _, signature := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`3`)} {
		input := messageStart() + blockStart(0, map[string]any{"type": "thinking", "signature": "ignored"})
		input += blockDelta(0, map[string]any{"type": "thinking_delta", "thinking": nil})
		delta := map[string]any{"type": "signature_delta"}
		if signature != nil {
			delta["signature"] = signature
		}
		input += blockDelta(0, delta) + blockStop(0) + terminal("end_turn", true)
		message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), nil)
		if err != nil || len(message.Blocks()) != 1 || message.Blocks()[0].String("thinking") != "null" || string(message.Blocks()[0].Get("signature")) != string(signature) {
			t.Fatalf("reference thinking += / signature assignment changed: %#v, %v", message, err)
		}
	}
}

func TestAnthropicProxyNonStringToolFragmentRemainsValidationInput(t *testing.T) {
	input := messageStart() + blockStart(0, map[string]any{"type": "tool_use", "id": "proxy-call", "name": "FixtureWrite"})
	input += blockDelta(0, map[string]any{"type": "input_json_delta", "partial_json": 3}) + blockStop(0) + terminal("tool_use", true)
	message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), nil)
	if err != nil || len(message.Blocks()) != 1 || string(message.Blocks()[0].Input()) != "3" {
		t.Fatalf("numeric proxy fragment was silently rejected or normalized: %#v, %v", message, err)
	}
	input = messageStart() + blockStart(0, map[string]any{"type": "tool_use", "id": "proxy-call", "name": "FixtureWrite"})
	input += blockDelta(0, map[string]any{"type": "input_json_delta", "partial_json": nil}) + blockStop(0) + terminal("tool_use", true)
	message, err = DecodeAnthropicStream(context.Background(), strings.NewReader(input), nil)
	assertStreamError(t, err, StreamInvalidEvent)
	if len(message.Blocks()) != 0 {
		t.Fatal("null partial_json bypassed the reference length-access failure")
	}
}

func TestAnthropicTerminalPresenceUsesNonNullInsteadOfTruthiness(t *testing.T) {
	for _, reason := range []any{"", 3, map[string]any{"future": true}} {
		input := messageStart() + textBlock(0, "visible", true)
		input += sseJSON("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": reason}})
		input += sseJSON("message_stop", map[string]any{"type": "message_stop"})
		message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), nil)
		raw, _ := json.Marshal(reason)
		if err != nil || string((*message)["stop_reason"]) != string(raw) || message.Text() != "visible" {
			t.Fatalf("non-null terminal was rewritten or rejected: %#v, %v", message, err)
		}
	}
}

func TestJoinJavaScriptValuesKeepsRecoveryArrayJoinBehavior(t *testing.T) {
	values := []json.RawMessage{
		json.RawMessage(`null`), json.RawMessage(`1e400`), json.RawMessage(`{"proxy":true}`),
		json.RawMessage(`[1,null,true]`), nil, json.RawMessage(`"中文😀"`),
	}
	joined, err := JoinJavaScriptValues(values, "\n")
	if err != nil || joined != "\nInfinity\n[object Object]\n1,,true\n\n中文😀" {
		t.Fatalf("reference Recovery join semantics changed: %q, %v", joined, err)
	}
	if joined, err := JoinJavaScriptValues(nil, "\n"); err != nil || joined != "" {
		t.Fatalf("empty content unexpectedly changed: %q, %v", joined, err)
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`{`), json.RawMessage(`true false`)} {
		if joined, err := JoinJavaScriptValues([]json.RawMessage{raw}, "\n"); err == nil || joined != "" {
			t.Fatalf("malformed value produced text: %q, %v", joined, err)
		}
	}
}

func TestJoinJavaScriptValuesJSONKeepsUTF16HistoryBeforeTerminalReplacement(t *testing.T) {
	for _, test := range []struct {
		values    []json.RawMessage
		separator string
		quoted    string
		terminal  string
	}{
		{[]json.RawMessage{json.RawMessage(`"A\ud800B\udfffC"`)}, "\n", `"A\ud800B\udfffC"`, "A�B�C"},
		{[]json.RawMessage{json.RawMessage(`"\ud83d"`), json.RawMessage(`"\ude00"`)}, "\n", `"\ud83d\n\ude00"`, "�\n�"},
		{[]json.RawMessage{json.RawMessage(`"\ud83d"`), json.RawMessage(`"\ude00"`)}, "", `"😀"`, "😀"},
		{[]json.RawMessage{json.RawMessage(`["\ud83d",null,"\ude00",["\ud800",[],"\udfff"]]`)}, "\n", `"\ud83d,,\ude00,\ud800,,\udfff"`, "�,,�,�,,�"},
		{[]json.RawMessage{json.RawMessage(`"\ud800"`), nil, json.RawMessage(`null`), json.RawMessage(`1e400`), json.RawMessage(`{"proxy":true}`)}, "/", `"\ud800///Infinity/[object Object]"`, "�///Infinity/[object Object]"},
		{nil, "\n", `""`, ""},
	} {
		quoted, err := JoinJavaScriptValuesJSON(test.values, test.separator)
		if err != nil || string(quoted) != test.quoted {
			t.Fatalf("history code units were replaced: %s, %v; want %s", quoted, err, test.quoted)
		}
		terminal, err := JoinJavaScriptValues(test.values, test.separator)
		if err != nil || terminal != test.terminal {
			t.Fatalf("terminal output changed: %q, %v; want %q", terminal, err, test.terminal)
		}
	}
	if quoted, err := JoinJavaScriptValuesJSON([]json.RawMessage{json.RawMessage(`{`)}, "\n"); err == nil || quoted != nil {
		t.Fatalf("invalid history source produced a quoted result: %s, %v", quoted, err)
	}
}
