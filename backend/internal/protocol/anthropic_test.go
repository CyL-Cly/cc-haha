package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

func sseJSON(kind string, value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return "event: " + kind + "\ndata: " + string(encoded) + "\n\n"
}

func messageStart() string {
	return sseJSON("message_start", map[string]any{
		"type": "message_start", "unknown_event_extension": true,
		"message": map[string]any{
			"id": "msg_fixture", "type": "message", "role": "assistant", "model": "fixture-model",
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"container": map[string]any{"id": "container_fixture"}, "future": json.RawMessage(`{"number":9007199254740993}`),
			"usage": map[string]any{
				"input_tokens": 100, "output_tokens": 0, "cache_creation_input_tokens": 20, "cache_read_input_tokens": 30,
				"service_tier": "standard", "server_tool_use": map[string]int{"web_search_requests": 1, "web_fetch_requests": 2},
				"initial_usage_null": nil,
				"cache_creation":     map[string]int{"ephemeral_1h_input_tokens": 5, "ephemeral_5m_input_tokens": 15},
			},
		},
	})
}

func blockStart(index int, block any) string {
	return sseJSON("content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": block})
}

func blockDelta(index int, delta any) string {
	return sseJSON("content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": delta})
}

func blockStop(index int) string {
	return sseJSON("content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

func textBlock(index int, text string, completed bool) string {
	result := blockStart(index, map[string]any{"type": "text", "text": "duplicated start text", "future_block": true})
	result += blockDelta(index, map[string]any{"type": "text_delta", "text": text})
	if completed {
		result += blockStop(index)
	}
	return result
}

func toolBlock(index int, id, raw string, completed bool) string {
	result := blockStart(index, map[string]any{"type": "tool_use", "id": id, "name": "FixtureWrite", "input": map[string]any{"ignored_start": true}, "caller": map[string]any{"type": "direct"}})
	// Fragment the JSON arguments at a rune boundary, independent of the
	// additional arbitrary UTF-8 fragmentation in the transport reader.
	runes := []rune(raw)
	for _, fragment := range []string{string(runes[:len(runes)/2]), string(runes[len(runes)/2:])} {
		result += blockDelta(index, map[string]any{"type": "input_json_delta", "partial_json": fragment})
	}
	if completed {
		result += blockStop(index)
	}
	return result
}

func terminal(reason string, stop bool) string {
	result := sseJSON("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": reason, "stop_sequence": "fixture-stop"}, "usage": map[string]int{"output_tokens": 7}})
	if stop {
		result += sseJSON("message_stop", map[string]any{"type": "message_stop"})
	}
	return result
}

func assertStreamError(t *testing.T, err error, code StreamErrorCode) *StreamError {
	t.Helper()
	var streamErr *StreamError
	if !errors.As(err, &streamErr) || streamErr.Code != code {
		t.Fatalf("wanted %s, got %T: %v", code, err, err)
	}
	return streamErr
}

func TestAnthropicCommitsOnlyAfterFullTerminalAndPreservesIdentity(t *testing.T) {
	rawInput := `{"file_path":"fixture/中文🙂.txt","content":"written once","counter":9007199254740993}`
	// Existing TS normalizes tool arguments through JSON.parse, so the
	// model-visible integer follows JavaScript's double precision.
	expectedInput := `{"file_path":"fixture/中文🙂.txt","content":"written once","counter":9007199254740992}`
	input := messageStart() + textBlock(0, "开始🙂", true) + toolBlock(1, "call_fixture", rawInput, true) + terminal("tool_use", true)
	for step := 1; step <= 32; step++ {
		messageStopSeen := false
		commits := 0
		rawEvents := 0
		message, err := DecodeAnthropicStream(context.Background(), &fragmentReader{data: []byte(input), step: step}, func(update StreamUpdate) error {
			if len(update.Event) > 0 {
				rawEvents++
				var event map[string]json.RawMessage
				if err := json.Unmarshal(update.Event, &event); err != nil {
					t.Fatal(err)
				}
				if stringField(event, "type") == "message_stop" {
					messageStopSeen = true
				}
			}
			if update.Message != nil {
				commits++
				if !messageStopSeen {
					t.Fatal("assistant committed before message_stop")
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("fragment width %d: %v", step, err)
		}
		if message == nil || message.String("id") != "msg_fixture" || message.String("model") != "fixture-model" || message.String("stop_reason") != "tool_use" || message.String("stop_sequence") != "fixture-stop" {
			t.Fatalf("provider identity or terminal lost: %#v", message)
		}
		if commits != 1 || rawEvents != 10 || message.Text() != "开始🙂" {
			t.Fatalf("incorrect delivery: commits=%d events=%d text=%q", commits, rawEvents, message.Text())
		}
		blocks := message.Blocks()
		if len(blocks) != 2 || blocks[1].String("id") != "call_fixture" || blocks[1].String("name") != "FixtureWrite" || string(blocks[1].Input()) != expectedInput {
			t.Fatalf("tool input or identity changed: %#v", blocks)
		}
		if string((*message)["future"]) != `{"number":9007199254740993}` || string(blocks[1].Get("caller")) != `{"type":"direct"}` || string(blocks[0].Get("future_block")) != "true" {
			t.Fatal("unknown message/block fields were dropped")
		}
		if message.Usage().Int64("input_tokens") != 100 || message.Usage().Int64("output_tokens") != 7 {
			t.Fatalf("usage lost: %#v", message.Usage())
		}
	}
}

func TestAnthropicDuplicateStopsAndDistinctIndexToolIDsExecuteOnce(t *testing.T) {
	input := messageStart() + toolBlock(0, "call_fixture", `{"path":"first"}`, true)
	input += blockStop(0) + blockStop(0)
	input += toolBlock(1, "call_fixture", `{"path":"duplicate"}`, true)
	input += terminal("tool_use", true)
	message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	if blocks := message.Blocks(); len(blocks) != 1 || string(blocks[0].Input()) != `{"path":"first"}` {
		t.Fatalf("duplicate tool was committed: %#v", blocks)
	}
}

func TestAnthropicIncompleteEOFNeverCommitsLocalTools(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
	}{
		{"completed block without terminal", messageStart() + toolBlock(0, "call_fixture", `{}`, true)},
		{"content after terminal reason", messageStart() + toolBlock(0, "call_fixture", `{}`, true) + terminal("tool_use", false) + blockStop(0)},
		{"message stop without terminal reason", messageStart() + toolBlock(0, "call_fixture", `{}`, true) + sseJSON("message_stop", map[string]any{"type": "message_stop"})},
		{"unfinished tool with terminal", messageStart() + toolBlock(0, "call_fixture", `{}`, false) + terminal("tool_use", true)},
		{"empty tool use", messageStart() + terminal("tool_use", true)},
	} {
		t.Run(test.name, func(t *testing.T) {
			message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(test.input), func(update StreamUpdate) error {
				if update.Message != nil {
					for _, block := range update.Message.Blocks() {
						if block.String("type") == "tool_use" {
							t.Fatal("failed stream exposed executable tool")
						}
					}
				}
				return nil
			})
			assertStreamError(t, err, StreamIncomplete)
			if message == nil || len(message.Blocks()) != 0 {
				t.Fatalf("failed tool appeared in returned content: %#v", message)
			}
		})
	}
}

func TestAnthropicTerminalReasonCommitsCompletedToolWithoutMessageStop(t *testing.T) {
	for _, suffix := range []string{
		"",
		sseJSON("ping", map[string]any{"type": "ping"}),
		sseJSON("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": nil}, "usage": map[string]any{"output_tokens": 9}}),
		"event: message_stop\ndata: {\"type\":\"message_stop\"}",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\r\n",
	} {
		commits := 0
		wire := messageStart() + toolBlock(0, "terminal-tool", `{"path":"once"}`, true) + terminal("tool_use", false) + suffix
		message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(wire), func(update StreamUpdate) error {
			if update.Message != nil {
				commits++
			}
			return nil
		})
		if err != nil || message == nil || len(message.Blocks()) != 1 || commits != 1 || string(message.Blocks()[0].Input()) != `{"path":"once"}` {
			t.Fatalf("terminal reason with trailing %q did not retain the completed tool once: %#v %v commits=%d", suffix, message, err, commits)
		}
	}
}

func TestAnthropicOutputLimitKeepsCompletedTextAndThinkingButDropsTool(t *testing.T) {
	thinking := blockStart(0, map[string]any{"type": "thinking", "thinking": "ignored", "signature": "ignored"})
	thinking += blockDelta(0, map[string]any{"type": "thinking_delta", "thinking": "plan"})
	thinking += blockDelta(0, map[string]any{"type": "signature_delta", "signature": "first"})
	thinking += blockDelta(0, map[string]any{"type": "signature_delta", "signature": "final"}) + blockStop(0)
	for _, reason := range []string{"max_tokens", "model_context_window_exceeded"} {
		for _, messageStop := range []bool{false, true} {
			input := messageStart() + thinking + textBlock(1, "completed text", true)
			input += textBlock(2, "unfinished output", false) + toolBlock(3, "truncated-tool", `{"content":"looks valid"}`, true)
			input += terminal(reason, messageStop)
			message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), nil)
			streamErr := assertStreamError(t, err, StreamOutputLimit)
			if streamErr.StopReason != reason || !streamErr.TruncatedToolUse {
				t.Fatalf("lost truncation semantics: %#v", streamErr)
			}
			blocks := message.Blocks()
			if len(blocks) != 2 || blocks[0].String("thinking") != "plan" || blocks[0].String("signature") != "final" || message.Text() != "completed text" {
				t.Fatalf("output limit changed safe content: %#v", blocks)
			}
		}
	}
	message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(messageStart()+textBlock(0, "continue me", true)+terminal("max_tokens", false)), nil)
	if assertStreamError(t, err, StreamOutputLimit).TruncatedToolUse || message.Text() != "continue me" {
		t.Fatal("ordinary text output limit became a tool truncation")
	}
}

func TestAnthropicEOFDisplayTextPreservedWithoutManufacturingThinking(t *testing.T) {
	input := messageStart() + textBlock(0, "visible partial", false)
	input += blockStart(1, map[string]any{"type": "thinking"}) + blockDelta(1, map[string]any{"type": "thinking_delta", "thinking": "unsigned private thought"})
	message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), nil)
	assertStreamError(t, err, StreamIncomplete)
	if message.Text() != "visible partial" || len(message.Blocks()) != 1 {
		t.Fatalf("lost text or manufactured thinking: %#v", message.Blocks())
	}
	// TS preservePartialText may complete an unfinished text block when the
	// response otherwise has a real terminal and message_stop.
	message, err = DecodeAnthropicStream(context.Background(), strings.NewReader(messageStart()+textBlock(0, "terminal text", false)+terminal("end_turn", true)), nil)
	if err != nil || message.Text() != "terminal text" {
		t.Fatalf("legitimate text terminal rejected: %#v, %v", message, err)
	}
}

func TestAnthropicEmptyEndTurnAndWhitespaceRemainValid(t *testing.T) {
	for _, content := range []string{"", textBlock(0, " \n\t", true)} {
		message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(messageStart()+content+terminal("end_turn", true)), nil)
		if err != nil || message.String("stop_reason") != "end_turn" {
			t.Fatalf("legitimate empty end_turn rejected: %#v, %v", message, err)
		}
		if content != "" && message.Text() != " \n\t" {
			t.Fatal("whitespace response changed")
		}
	}
	for _, input := range []string{"", "not SSE", messageStart()} {
		_, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), nil)
		assertStreamError(t, err, StreamIncomplete)
	}
}

func TestAnthropicProviderAndTransportErrorsKeepPayloadWithoutTools(t *testing.T) {
	payload := json.RawMessage(`{"type":"overloaded_error","message":"fixture overload","retry_after":4,"request_id":"upstream-request"}`)
	input := messageStart() + textBlock(0, "safe text", true) + toolBlock(1, "unsafe-tool", `{}`, true)
	input += sseJSON("error", map[string]any{"type": "error", "error": payload})
	message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), nil)
	streamErr := assertStreamError(t, err, StreamProviderError)
	if string(streamErr.ProviderError) != string(payload) || streamErr.Error() != "fixture overload" || message.Text() != "safe text" || len(message.Blocks()) != 1 {
		t.Fatalf("provider error semantics lost: %#v, %#v", streamErr, message)
	}
	cause := errors.New("fixture transport reset")
	message, err = DecodeAnthropicStream(context.Background(), &readerThenError{data: []byte(messageStart() + textBlock(0, "partial text", false) + toolBlock(1, "unsafe-tool", `{}`, true)), err: cause}, nil)
	assertStreamError(t, err, StreamTransport)
	if !errors.Is(err, cause) || message.Text() != "partial text" || len(message.Blocks()) != 1 {
		t.Fatalf("transport error changed partial result: %#v, %v", message, err)
	}
}

func TestAnthropicUsageIsCumulativeAndUsageOnlyDeltaKeepsTerminal(t *testing.T) {
	input := messageStart() + textBlock(0, "done", true) + terminal("end_turn", true)
	input += sseJSON("message_delta", map[string]any{
		"type": "message_delta", "delta": map[string]any{"stop_reason": nil, "stop_sequence": nil},
		"usage": map[string]any{
			"input_tokens": 0, "cache_creation_input_tokens": 0, "cache_read_input_tokens": nil, "output_tokens": 9,
			"server_tool_use": map[string]any{"web_search_requests": 0}, "service_tier": "wrong-tier",
			"cache_creation": map[string]any{"ephemeral_1h_input_tokens": nil, "ephemeral_5m_input_tokens": 0},
			"unknown_usage":  json.RawMessage(`9007199254740993`),
			"unknown_null":   nil,
		},
	})
	message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	if message.String("stop_reason") != "end_turn" || message.String("stop_sequence") != "fixture-stop" {
		t.Fatal("usage-only delta erased terminal")
	}
	usage := message.Usage()
	if usage.Int64("input_tokens") != 100 || usage.Int64("cache_creation_input_tokens") != 20 || usage.Int64("cache_read_input_tokens") != 30 || usage.Int64("output_tokens") != 9 || usage.Int64("unknown_usage") != 9007199254740993 {
		t.Fatalf("usage was summed, cleared, or rounded: %#v", usage)
	}
	if string(usage["server_tool_use"]) != `{"web_fetch_requests":2,"web_search_requests":0}` || string(usage["service_tier"]) != `"standard"` || string(usage["cache_creation"]) != `{"ephemeral_1h_input_tokens":5,"ephemeral_5m_input_tokens":0}` {
		t.Fatalf("nested or latched usage changed: %#v", usage)
	}
	if string(usage["initial_usage_null"]) != "null" || string(usage["unknown_null"]) != "null" {
		t.Fatal("unknown usage null extension was dropped")
	}
}

func TestAnthropicMalformedToolArgumentsRemainValidationFeedback(t *testing.T) {
	for _, raw := range []string{`{"path":"bad\q"}`, `{"x":`, " ", "😀{"} {
		message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(messageStart()+toolBlock(0, "bad-args", raw, true)+terminal("tool_use", true)), nil)
		if err != nil {
			t.Fatal(err)
		}
		input := message.Blocks()[0].Input()
		var marker struct {
			Unparsed struct {
				Raw string `json:"raw"`
				Len int    `json:"len"`
			} `json:"__unparsedToolInput"`
		}
		if err := json.Unmarshal(input, &marker); err != nil || marker.Unparsed.Raw != raw || marker.Unparsed.Len != len(utf16.Encode([]rune(raw))) {
			t.Fatalf("malformed input became executable {}: %s, %v", input, err)
		}
	}
	// JS slice(0, 2048) may end on a high surrogate. Preserve that exact
	// escaped code unit and use UTF-16 length, not UTF-8 bytes/rune count.
	raw := strings.Repeat("a", 2047) + "😀{" + strings.Repeat("b", 30)
	marker := string(normalizeToolArguments(raw, true))
	if !strings.Contains(marker, `\ud83d"`) || !strings.Contains(marker, `"len":2080`) || strings.Contains(marker, "�") {
		t.Fatalf("JS preview/length changed: %s", marker)
	}
}

func TestAnthropicValidPrimitiveAndEmptyInputsKeepCurrentNormalization(t *testing.T) {
	for _, raw := range []string{"", "null", " null ", "[]", `"nested string"`, "false", `{"extension":9007199254740993}`} {
		message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(messageStart()+toolBlock(0, "call", raw, true)+terminal("tool_use", true)), nil)
		if err != nil {
			t.Fatal(err)
		}
		expected := raw
		if raw == "" || strings.TrimSpace(raw) == "null" {
			expected = "{}"
		} else if raw == `{"extension":9007199254740993}` {
			expected = `{"extension":9007199254740992}`
		}
		if got := string(message.Blocks()[0].Input()); got != expected {
			t.Fatalf("input %q: got %s, want %s", raw, got, expected)
		}
	}
}

func TestAnthropicUnknownBlocksAndRawEventsRetainProviderFields(t *testing.T) {
	input := messageStart() + blockStart(0, json.RawMessage(`{"type":"redacted_thinking","data":"signed-data","future":{"n":9007199254740993}}`)) + blockStop(0) + terminal("end_turn", true)
	var raw []json.RawMessage
	message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(input), func(update StreamUpdate) error {
		if len(update.Event) > 0 {
			raw = append(raw, update.Event)
		}
		return nil
	})
	if err != nil || len(raw) != 5 {
		t.Fatalf("unknown block rejected or events lost: %v, %d", err, len(raw))
	}
	if !bytesContain(raw[0], `"unknown_event_extension":true`) || string(message.Blocks()[0].Get("future")) != `{"n":9007199254740993}` {
		t.Fatal("unknown fields lost")
	}
	// A complete server tool is retained on a failed stream. Unlike a local
	// tool, its execution already happened upstream and cannot be undone.
	server := blockStart(0, map[string]any{"type": "server_tool_use", "id": "server-id", "name": "web_search", "input": map[string]any{}})
	server += blockDelta(0, map[string]any{"type": "input_json_delta", "partial_json": `{"query":"fixture"}`}) + blockStop(0)
	message, err = DecodeAnthropicStream(context.Background(), strings.NewReader(messageStart()+server), nil)
	assertStreamError(t, err, StreamIncomplete)
	if len(message.Blocks()) != 1 || message.Blocks()[0].String("type") != "server_tool_use" || string(message.Blocks()[0].Input()) != `{"query":"fixture"}` {
		t.Fatal("completed server work disappeared")
	}
}

func TestAnthropicInvalidEventsDoNotCompleteTools(t *testing.T) {
	for _, invalid := range []string{
		"event: content_block_delta\ndata: {not json}\n\n",
		"data: []\n\n",
		"data: null\n\n",
		"data: {}\n\n",
		"data: \xff\n\n",
		blockDelta(9, map[string]any{"type": "text_delta", "text": "orphan"}),
		blockStop(9),
		blockDelta(0, map[string]any{"type": "input_json_delta", "partial_json": "{}"}),
		sseJSON("content_block_start", map[string]any{"type": "content_block_start", "index": 1.5, "content_block": map[string]any{"type": "text"}}),
		sseJSON("content_block_stop", map[string]any{"type": "content_block_stop", "index": nil}),
	} {
		message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(messageStart()+textBlock(0, "partial", false)+invalid), nil)
		assertStreamError(t, err, StreamInvalidEvent)
		if message == nil || message.Text() != "partial" {
			t.Fatal("validation error discarded visible text")
		}
	}
}

func TestAnthropicCancellationAndConsumerErrorsNeverManufactureSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	message, err := DecodeAnthropicStream(ctx, strings.NewReader(messageStart()+terminal("end_turn", true)), nil)
	assertStreamError(t, err, StreamAborted)
	if !errors.Is(err, context.Canceled) || message != nil {
		t.Fatalf("canceled decode succeeded: %#v, %v", message, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	calls := 0
	message, err = DecodeAnthropicStream(ctx, strings.NewReader(messageStart()+textBlock(0, "text before abort", false)+toolBlock(1, "call", `{}`, true)+terminal("tool_use", true)), func(update StreamUpdate) error {
		calls++
		if len(update.Event) > 0 && bytesContain(update.Event, `"partial_json"`) {
			cancel()
		}
		return nil
	})
	assertStreamError(t, err, StreamAborted)
	if !errors.Is(err, context.Canceled) || message.Text() != "text before abort" || len(message.Blocks()) != 1 {
		t.Fatalf("abort lost text or exposed tools: %#v, %v", message, err)
	}
	cause := errors.New("consumer closed")
	calls = 0
	_, err = DecodeAnthropicStream(context.Background(), strings.NewReader(messageStart()+terminal("end_turn", true)), func(update StreamUpdate) error {
		calls++
		return cause
	})
	if !errors.Is(err, cause) || calls != 1 {
		t.Fatalf("consumer error became success or callbacks continued: %v, %d", err, calls)
	}
}

func TestAnthropicCRLFMultilineRetainsTerminalReasonWithUnterminatedStop(t *testing.T) {
	input := strings.ReplaceAll(messageStart()+textBlock(0, "中文🙂", true)+terminal("end_turn", true), "\n", "\r\n")
	// A multiline JSON frame retains literal line boundaries between data
	// fields, rather than concatenating tokens without whitespace.
	input = strings.Replace(input, `data: {"delta":`, "data: {\r\ndata: \"delta\":", 1)
	message, err := DecodeAnthropicStream(context.Background(), &fragmentReader{data: []byte(input), step: 1}, nil)
	if err != nil || message.Text() != "中文🙂" {
		t.Fatalf("transport formatting changed message: %#v, %v", message, err)
	}
	input = strings.TrimSuffix(input, "\r\n\r\n")
	message, err = DecodeAnthropicStream(context.Background(), &fragmentReader{data: []byte(input), step: 1}, nil)
	if err != nil || message == nil || message.Text() != "中文🙂" {
		t.Fatalf("unterminated stop lost the preceding terminal reason: %#v %v", message, err)
	}
}

func TestAnthropicSDKDecodesInvalidUTF8InsideStrings(t *testing.T) {
	input := messageStart() + "data: {\"type\":\"ping\",\"text\":\"\xe2\x82\"}\n\n" + textBlock(0, "decoded", true) + terminal("end_turn", true)
	var ping json.RawMessage
	message, err := DecodeAnthropicStream(context.Background(), &fragmentReader{data: []byte(input), step: 1}, func(update StreamUpdate) error {
		if bytesContain(update.Event, `"type":"ping"`) {
			ping = update.Event
		}
		return nil
	})
	if err != nil || message.Text() != "decoded" || string(ping) != `{"type":"ping","text":"�"}` {
		t.Fatalf("quoted UTF-8 corruption differs from SDK: %s, %#v, %v", ping, message, err)
	}
}

func TestAnthropicTerminalReasonDoesNotDispatchPendingStopFrame(t *testing.T) {
	base := messageStart() + textBlock(0, "visible", true) + toolBlock(1, "completed", `{}`, true) + terminal("tool_use", false)
	for _, ending := range []string{"", "\n", "\r", "\r\n", "\n\n", "\r\r", "\r\n\r\n"} {
		stopEvents := 0
		message, err := DecodeAnthropicStream(context.Background(), &fragmentReader{data: []byte(base + "data: {\"type\":\"message_stop\"}" + ending), step: 1}, func(update StreamUpdate) error {
			if bytesContain(update.Event, `"type":"message_stop"`) {
				stopEvents++
			}
			return nil
		})
		complete := ending == "\n\n" || ending == "\r\r" || ending == "\r\n\r\n"
		wantStops := 0
		if complete {
			wantStops = 1
		}
		if err != nil || message == nil || len(message.Blocks()) != 2 || message.Text() != "visible" || stopEvents != wantStops {
			t.Fatalf("terminal reason with stop ending %q changed content or synthesized an SSE frame: %#v %v stopEvents=%d want=%d", ending, message, err, stopEvents, wantStops)
		}
	}
}

func TestAnthropicInputsAndUnknownMessageFieldsAreIndependentCopies(t *testing.T) {
	message, err := DecodeAnthropicStream(context.Background(), strings.NewReader(messageStart()+toolBlock(0, "call", `{"x":1}`, true)+terminal("tool_use", true)), nil)
	if err != nil {
		t.Fatal(err)
	}
	first := message.Blocks()
	input := first[0].Input()
	input[0] = 'X'
	first[0].Set("name", json.RawMessage(`"mutated"`))
	if string(message.Blocks()[0].Input()) != `{"x":1}` || message.Blocks()[0].String("name") != "FixtureWrite" {
		t.Fatal("consumer mutated committed message")
	}
	if !reflect.DeepEqual(message.Usage(), message.Usage()) {
		t.Fatal("unstable usage projection")
	}
}

func bytesContain(value []byte, expected string) bool {
	return strings.Contains(string(value), expected)
}

// Compile-time assertions keep the fixture readers honest about transport I/O.
var _ io.Reader = (*fragmentReader)(nil)
var _ io.Reader = (*readerThenError)(nil)
