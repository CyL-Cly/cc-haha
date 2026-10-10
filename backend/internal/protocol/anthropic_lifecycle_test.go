package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestStreamContentKeepsPropertyOrderAndUTF16AcrossDeltas(t *testing.T) {
	start := json.RawMessage(`{"future":{"z":1,"a":2},"\ud800":"high","\udfff":"low","�":"replacement","text":"ignored","type":"text"}`)
	wire := messageStart() + blockStart(0, start)
	for _, text := range []json.RawMessage{json.RawMessage(`"\ud800"`), json.RawMessage(`"x\udfff"`), json.RawMessage(`"\ud83d"`), json.RawMessage(`"\ude00"`), json.RawMessage(`["\ud800",null,"\udfff"]`)} {
		wire += blockDelta(0, map[string]any{"type": "text_delta", "text": text})
	}
	wire += blockStop(0) + terminal("end_turn", true)
	message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(wire), nil)
	if err != nil || message == nil {
		t.Fatal(message, err)
	}
	want := `[{"future":{"z":1,"a":2},"\ud800":"high","\udfff":"low","�":"replacement","text":"\ud800x\udfff😀\ud800,,\udfff","type":"text"}]`
	if string((*message)["content"]) != want {
		t.Fatalf("model-visible content changed: want=%s actual=%s", want, (*message)["content"])
	}
	first := message.Blocks()
	first[0].Set("future", json.RawMessage(`null`))
	if string((*message)["content"]) != want {
		t.Fatal("caller changed committed provider history")
	}
}

func TestUndefinedSignatureRetainsItsExistingPropertySlot(t *testing.T) {
	wire := messageStart() + blockStart(0, json.RawMessage(`{"signature":"ignored","future":true,"type":"thinking","thinking":"ignored"}`))
	wire += blockDelta(0, map[string]any{"type": "thinking_delta", "thinking": "plan"})
	wire += blockDelta(0, map[string]any{"type": "signature_delta"})
	wire += blockDelta(0, map[string]any{"type": "signature_delta", "signature": "final"})
	wire += blockStop(0) + terminal("end_turn", true)
	message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(wire), nil)
	if err != nil || message == nil || string((*message)["content"]) != `[{"signature":"final","future":true,"type":"thinking","thinking":"plan"}]` {
		t.Fatal(message, err)
	}
}

// A proxy can emit a valid-looking completed tool before a later malformed
// frame. A parser failure must keep earlier display text but never turn that
// tool into an executable commit, regardless of which frame broke the stream.
func TestAnthropicMalformedUpstreamRetainsDisplayWithoutCommittingTools(t *testing.T) {
	prefix := messageStart() + toolBlock(0, "pending-tool", `{"path":"fixture"}`, true) + textBlock(1, "visible text", false)
	for _, test := range []struct {
		name  string
		frame string
	}{
		{"second message", messageStart()},
		{"duplicate unfinished block", blockStart(1, map[string]any{"type": "text"})},
		{"missing block object", blockStart(2, nil)},
		{"block object is array", blockStart(2, []string{"text"})},
		{"missing block type", blockStart(2, map[string]any{"text": "bad"})},
		{"missing local tool identity", blockStart(2, map[string]any{"type": "tool_use", "name": "FixtureWrite"})},
		{"missing server tool identity", blockStart(2, map[string]any{"type": "server_tool_use", "name": "web_search"})},
		{"invalid delta index", sseJSON("content_block_delta", map[string]any{"type": "content_block_delta", "index": -1, "delta": map[string]any{"type": "text_delta", "text": "bad"}})},
		{"missing delta object", blockDelta(1, nil)},
		{"array delta object", blockDelta(1, []string{"text_delta"})},
		{"text delta on tool", blockStart(2, map[string]any{"type": "tool_use", "id": "second-tool"}) + blockDelta(2, map[string]any{"type": "text_delta", "text": "bad"})},
		{"thinking delta on text", blockDelta(1, map[string]any{"type": "thinking_delta", "thinking": "bad"})},
		{"signature delta on text", blockDelta(1, map[string]any{"type": "signature_delta", "signature": "bad"})},
		{"missing tool argument fragment", blockStart(2, map[string]any{"type": "tool_use", "id": "second-tool"}) + blockDelta(2, map[string]any{"type": "input_json_delta"})},
		{"missing terminal delta", sseJSON("message_delta", map[string]any{"type": "message_delta", "delta": nil})},
		{"terminal delta is array", sseJSON("message_delta", map[string]any{"type": "message_delta", "delta": []string{"end_turn"}})},
		{"usage is array", sseJSON("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{}, "usage": []int{1}})},
	} {
		t.Run(test.name, func(t *testing.T) {
			commits := 0
			message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(prefix+test.frame+terminal("tool_use", true)), func(update StreamUpdate) error {
				if update.Message != nil {
					commits++
					if update.Message.Text() != "visible text" || len(update.Message.Blocks()) != 1 {
						t.Fatalf("invalid upstream exposed tools or lost display: %#v", update.Message.Blocks())
					}
				}
				return nil
			})
			assertStreamError(t, err, StreamInvalidEvent)
			if message == nil || message.Text() != "visible text" || len(message.Blocks()) != 1 || commits != 1 {
				t.Fatalf("failed decode changed recovery content: %#v, commits=%d", message, commits)
			}
		})
	}
}

func TestAnthropicInvalidStartCannotManufactureAssistantRecord(t *testing.T) {
	for _, input := range []string{
		blockStart(0, map[string]any{"type": "text"}),
		terminal("end_turn", true),
		sseJSON("message_start", map[string]any{"type": "message_start"}),
		sseJSON("message_start", map[string]any{"type": "message_start", "message": nil}),
		sseJSON("message_start", map[string]any{"type": "message_start", "message": []string{"assistant"}}),
	} {
		commits := 0
		message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), func(update StreamUpdate) error {
			if update.Message != nil {
				commits++
			}
			return nil
		})
		assertStreamError(t, err, StreamInvalidEvent)
		if message != nil || commits != 0 {
			t.Fatalf("invalid start manufactured assistant content: %#v, %d", message, commits)
		}
	}
}

func TestAnthropicCompletedIndexReplaysRemainInert(t *testing.T) {
	input := messageStart() + toolBlock(0, "original", `{"path":"first"}`, true)
	// Proxy retries can replay an index with a different ID or emit a late
	// malformed delta. Both must remain inert once that index is complete.
	input += blockStart(0, map[string]any{"type": "tool_use", "id": "replayed", "name": "FixtureWrite"})
	input += blockDelta(0, map[string]any{"type": "input_json_delta", "partial_json": `{"path":"second"}`}) + blockStop(0)
	input += terminal("tool_use", true)
	message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), nil)
	if err != nil || len(message.Blocks()) != 1 || message.Blocks()[0].String("id") != "original" || string(message.Blocks()[0].Input()) != `{"path":"first"}` {
		t.Fatalf("completed index was replaced or duplicated: %#v, %v", message, err)
	}
}

func TestAnthropicToolIDsCompareUTF16WithoutReplacement(t *testing.T) {
	for _, kind := range []string{"tool_use", "server_tool_use"} {
		t.Run(kind, func(t *testing.T) {
			ids := []json.RawMessage{json.RawMessage(`"\ud800"`), json.RawMessage(`"\udfff"`), json.RawMessage(`"�"`), json.RawMessage(`"\uD800"`), json.RawMessage(`"😀"`), json.RawMessage(`"\ud83d\ude00"`)}
			wire := messageStart()
			for index, id := range ids {
				wire += blockStart(index, map[string]any{"type": kind, "id": id, "name": "FixtureWrite"})
				wire += blockDelta(index, map[string]any{"type": "input_json_delta", "partial_json": `{}`}) + blockStop(index)
			}
			message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(wire+terminal("tool_use", true)), nil)
			if err != nil {
				t.Fatal(err)
			}
			blocks := message.Blocks()
			if len(blocks) != 4 {
				t.Fatalf("distinct UTF-16 IDs collapsed or real replays committed: wanted 4, got %d", len(blocks))
			}
			for index, original := range []int{0, 1, 2, 4} {
				if string(blocks[index].Get("id")) != string(ids[original]) {
					t.Fatalf("committed ID was re-encoded or replaced: %s != %s", blocks[index].Get("id"), ids[original])
				}
			}
		})
	}
}

func TestAnthropicProviderErrorWithoutStandardEnvelopeRemainsFailure(t *testing.T) {
	for _, test := range []struct {
		frame   string
		payload string
	}{
		{"event: error\ndata: {\"type\":\"gateway_failure\",\"trace_id\":\"fixture\"}\n\n", `{"type":"gateway_failure","trace_id":"fixture"}`},
		{"data: {\"type\":\"error\",\"error\":{\"code\":502}}\n\n", `{"code":502}`},
	} {
		message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(messageStart()+textBlock(0, "visible", true)+toolBlock(1, "uncommitted", `{}`, true)+test.frame), nil)
		streamErr := assertStreamError(t, err, StreamProviderError)
		if string(streamErr.ProviderError) != test.payload || streamErr.Error() != "Provider reported a stream error" || message.Text() != "visible" || len(message.Blocks()) != 1 {
			t.Fatalf("gateway error was treated as success or lost payload: %#v, %#v", streamErr, message)
		}
	}
	if got := (&StreamError{Code: StreamIncomplete}).Error(); got != string(StreamIncomplete) {
		t.Fatalf("empty provider detail lost typed error: %q", got)
	}
}

type cancelAtReadEnd struct {
	reader io.Reader
	cancel context.CancelFunc
}

func (reader *cancelAtReadEnd) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	if err != nil {
		reader.cancel()
	}
	return count, err
}

func TestAnthropicCancellationAtTransportEndWinsOverTerminal(t *testing.T) {
	input := messageStart() + textBlock(0, "visible", true) + toolBlock(1, "completed-tool", `{}`, true) + terminal("tool_use", true)
	for _, end := range []error{io.EOF, errors.New("request body closed")} {
		ctx, cancel := context.WithCancel(context.Background())
		reader := &cancelAtReadEnd{reader: &readerThenError{data: []byte(input), err: end}, cancel: cancel}
		message, err := DecodeAnthropicStream(ctx, reader, nil)
		assertStreamError(t, err, StreamAborted)
		if !errors.Is(err, context.Canceled) || message.Text() != "visible" || len(message.Blocks()) != 1 {
			t.Fatalf("cancel at transport end became executable success: %#v, %v", message, err)
		}
	}
}

func TestAnthropicFinalConsumerFailurePreservesCauseAndStopsDelivery(t *testing.T) {
	cause := errors.New("session journal unavailable")
	for _, suffix := range []string{terminal("end_turn", true), ""} {
		commits := 0
		_, err := DecodeAnthropicStream(context.Background(), strings.NewReader(messageStart()+textBlock(0, "visible", true)+suffix), func(update StreamUpdate) error {
			if update.Message != nil {
				commits++
				return cause
			}
			return nil
		})
		if !errors.Is(err, cause) || commits != 1 {
			t.Fatalf("final consumer rejection was swallowed or redelivered: %v, %d", err, commits)
		}
		if suffix == "" {
			assertStreamError(t, err, StreamIncomplete)
		}
	}
}

func TestAnthropicMalformedServerInputNormalizesWithoutLocalMarker(t *testing.T) {
	input := messageStart() + blockStart(0, map[string]any{"type": "server_tool_use", "id": "server-query", "name": "web_search"})
	input += blockDelta(0, map[string]any{"type": "input_json_delta", "partial_json": `{"query":`}) + blockStop(0) + terminal("end_turn", true)
	message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), nil)
	if err != nil || len(message.Blocks()) != 1 || string(message.Blocks()[0].Input()) != "{}" {
		t.Fatalf("server normalization diverged from safeParseJSON(input) ?? {}: %#v, %v", message, err)
	}
}

func TestAnthropicMalformedInputFeedbackEscapesEveryControlCharacter(t *testing.T) {
	raw := "{\"path\":\"中文🙂\"}\b\f\n\r\t\x00\x01\\\""
	message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(messageStart()+toolBlock(0, "bad-args", raw, true)+terminal("tool_use", true)), nil)
	if err != nil {
		t.Fatal(err)
	}
	var marker struct {
		Unparsed struct {
			Raw string `json:"raw"`
			Len int    `json:"len"`
		} `json:"__unparsedToolInput"`
	}
	if err := json.Unmarshal(message.Blocks()[0].Input(), &marker); err != nil || marker.Unparsed.Raw != raw {
		t.Fatalf("validation feedback cannot be transported losslessly: %s, %v", message.Blocks()[0].Input(), err)
	}
}

func TestAnthropicNullUsageDeltaDoesNotClearCountersOrNestedUsage(t *testing.T) {
	input := messageStart() + textBlock(0, "visible", true) + terminal("end_turn", true)
	input += sseJSON("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{}, "usage": map[string]any{
		"input_tokens": map[string]any{"invalid": true}, "cache_read_input_tokens": "not a counter", "output_tokens": nil,
		"server_tool_use": nil, "cache_creation": nil,
	}})
	message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	usage := message.Usage()
	if usage.Int64("input_tokens") != 100 || usage.Int64("cache_read_input_tokens") != 30 || usage.Int64("output_tokens") != 7 || string(usage["server_tool_use"]) != `{"web_fetch_requests":2,"web_search_requests":1}` || string(usage["cache_creation"]) != `{"ephemeral_1h_input_tokens":5,"ephemeral_5m_input_tokens":15}` {
		t.Fatalf("missing or invalid usage update cleared cumulative counters: %#v", usage)
	}
}
