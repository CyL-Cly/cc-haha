package protocol

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAnthropicToolJSONKeepsReferenceNumberAndObjectSemantics(t *testing.T) {
	for _, test := range []struct {
		input    string
		expected string
	}{
		{`{"integer":9007199254740993,"large":1e400,"negativeLarge":-1e400,"tiny":1e-400,"scientific":1e21,"negativeZero":-0,"nested":[9007199254740993]}`, `{"integer":9007199254740992,"large":null,"negativeLarge":null,"tiny":0,"scientific":1e+21,"negativeZero":0,"nested":[9007199254740992]}`},
		{" \r\n\t{ \"2\" : \"two\", \"x\":1, \"01\":\"leading zero\", \"1\":\"first\", \"4294967295\":\"non-index\", \"x\":2, \"0\":\"zero\", \"\u0033\":\"three\" } \t", `{"0":"zero","1":"first","2":"two","3":"three","x":2,"01":"leading zero","4294967295":"non-index"}`},
		{`[[],{},null,true,false,"",[0.000001,0.0000001,-1e21]]`, `[[],{},null,true,false,"",[0.000001,1e-7,-1e+21]]`},
		{`{"text":"\b\f\n\r\t\"\\\/\u0000中文\ud83d\ude00","high":"\ud800","low":"\udfff","\ud800":"key"}`, `{"text":"\b\f\n\r\t\"\\/\u0000中文😀","high":"\ud800","low":"\udfff","\ud800":"key"}`},
	} {
		input := messageStart() + toolBlock(0, "json-call", test.input, true) + terminal("tool_use", true)
		message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), nil)
		if err != nil || len(message.Blocks()) != 1 || string(message.Blocks()[0].Input()) != test.expected {
			t.Fatalf("JSON.parse / JSON.stringify compatibility changed:\ninput: %s\ngot: %s\nwant: %s\nerror: %v", test.input, message.Blocks()[0].Input(), test.expected, err)
		}
	}
}

func TestAnthropicToolUTF16FragmentsAndMalformedFeedbackPreserveLoneUnits(t *testing.T) {
	for _, test := range []struct {
		fragments []string
		expected  string
	}{
		{[]string{`"\ud800{"`}, `{"__unparsedToolInput":{"raw":"\ud800{","len":2}}`},
		{[]string{`"\udfff{"`}, `{"__unparsedToolInput":{"raw":"\udfff{","len":2}}`},
		{[]string{`"{\"value\":\"\ud800\"}"`}, `{"value":"\ud800"}`},
		// A surrogate pair can cross partial_json event boundaries, just
		// as an ordinary UTF-8 rune can cross network read boundaries.
		{[]string{`"{\"value\":\"\ud83d"`, `"\ude00\"}"`}, `{"value":"😀"}`},
	} {
		input := messageStart() + blockStart(0, map[string]any{"type": "tool_use", "id": "utf16-call", "name": "FixtureWrite"})
		for _, fragment := range test.fragments {
			input += blockDelta(0, map[string]any{"type": "input_json_delta", "partial_json": json.RawMessage(fragment)})
		}
		input += blockStop(0) + terminal("tool_use", true)
		message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), nil)
		if err != nil || len(message.Blocks()) != 1 || string(message.Blocks()[0].Input()) != test.expected {
			t.Fatalf("UTF-16 input or feedback was replaced: %#v, %v; want %s", message, err, test.expected)
		}
	}
}
