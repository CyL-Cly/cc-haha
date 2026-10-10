package providerproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func responsesEvent(kind string, value map[string]any) string {
	if value == nil {
		value = map[string]any{}
	}
	value["type"] = kind
	return "event:" + kind + "\ndata:" + string(responseJSON(value)) + "\n\n"
}
func responsesDone(output any) string {
	response := map[string]any{"status": "completed"}
	if output != nil {
		response["output"] = output
	}
	return responsesEvent("response.completed", map[string]any{"response": response})
}

type streamSourceRow struct {
	Input     string `json:"input"`
	Bytes     []int  `json:"bytes,omitempty"`
	ChunkSize int    `json:"chunkSize"`
	OAuth     bool   `json:"oauth"`
}

func TestActualResponsesSSETransportSegmentationAndTerminalFramesMatchSource(t *testing.T) {
	tool := map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "Write", "arguments": `{"path":"fixture","text":"你好\ud800"}`}
	startTool := responsesEvent("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "Write", "arguments": ""}})
	text := responsesEvent("response.content_part.added", map[string]any{"output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": ""}})
	inputs := []string{
		responsesDone([]any{}),
		responsesEvent("response.created", map[string]any{"response": map[string]any{"id": "actual-upstream", "model": "actual-created"}}) + text + responsesEvent("response.output_text.delta", map[string]any{"output_index": 0, "content_index": 0, "delta": "hello你好"}) + responsesEvent("response.output_text.done", map[string]any{"output_index": 0, "content_index": 0, "text": "hello你好"}) + responsesDone([]any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "hello你好"}}}}),
		startTool + responsesEvent("response.function_call_arguments.delta", map[string]any{"item_id": "fc_1", "delta": `{"path":`}) + responsesDone([]any{tool}),
		startTool + responsesEvent("response.function_call_arguments.done", map[string]any{"output_index": 0, "arguments": tool["arguments"]}) + responsesDone([]any{tool}),
		responsesDone([]any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "snapshot"}}}, tool}),
		responsesEvent("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"type": "reasoning", "id": "rs"}}) + responsesEvent("response.reasoning_summary_text.delta", map[string]any{"output_index": 0, "delta": "actual reasoning"}) + responsesDone([]any{map[string]any{"type": "reasoning", "id": "rs", "summary": []any{map[string]any{"text": "actual reasoning"}}, "encrypted_content": "opaque<keep>"}}),
		responsesDone([]any{map[string]any{"type": "reasoning", "summary": []any{map[string]any{"text": "summary-only"}}, "encrypted_content": "cipher"}}),
		startTool + responsesEvent("response.function_call_arguments.delta", map[string]any{"item_id": "fc_1", "delta": `{"partial":`}) + responsesEvent("response.incomplete", map[string]any{"response": map[string]any{"status": "incomplete", "incomplete_details": map[string]any{"reason": "max_output_tokens"}, "usage": map[string]any{"output_tokens": 12}}}),
		responsesEvent("response.failed", map[string]any{"response": map[string]any{"error": map[string]any{"type": "rate_limit_error", "message": "actual upstream failed"}}}),
		responsesEvent("error", map[string]any{"error": map[string]any{"code": "cyber_policy", "message": " policy stop "}}),
		"data:[DONE]\n\n",
		startTool,
		"event:response.output_text.delta\ndata:{broken}\n\n",
		responsesDone(nil),
		responsesEvent("response.completed", map[string]any{"response": map[string]any{"status": "completed", "output": map[string]any{}}}),
		startTool + responsesEvent("response.function_call_arguments.delta", map[string]any{"item_id": "fc_1", "delta": "[]"}) + responsesDone(nil),
		startTool + responsesEvent("response.function_call_arguments.delta", map[string]any{"item_id": "fc_1", "delta": `{"different":1}`}) + responsesDone([]any{tool}),
		text + responsesEvent("response.output_text.done", map[string]any{"output_index": 0, "content_index": 0, "text": "done"}) + responsesEvent("response.output_text.delta", map[string]any{"output_index": 0, "content_index": 0, "delta": "late"}) + responsesDone([]any{}),
		":comment\r\nevent: response.completed\r\nid:ignored\r\ndata: {\r\ndata: \"response\":{\"status\":\"completed\",\"output\":[]}\r\ndata: }\r\n\r\n",
		"data:{\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}",
	}

	for _, snapshot := range []string{` { "text": "你好\\ud800", "path": "fixture" } `, `{"path":"fixture","text":"你好\\ud800"}`} {
		inputs = append(inputs, startTool+responsesEvent("response.function_call_arguments.delta", map[string]any{"item_id": "fc_1", "delta": tool["arguments"]})+responsesDone([]any{map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "Write", "arguments": snapshot}}))
	}
	for _, argument := range []string{`{"n":-0}`, `{"n":1e999}`, `{"n":null}`, `{malformed\ud800`} {
		finalArgument := argument
		if argument == `{"n":-0}` {
			finalArgument = `{"n":0}`
		}
		inputs = append(inputs, startTool+responsesEvent("response.function_call_arguments.delta", map[string]any{"item_id": "fc_1", "delta": argument})+responsesDone([]any{map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "Write", "arguments": finalArgument}}))
	}
	inputs = append(inputs, responsesEvent("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"type": "function_call", "id": "fallback-id", "name": "Read", "arguments": `{}`}})+responsesDone(nil))
	inputs = append(inputs, startTool+responsesDone([]any{map[string]any{"type": "function_call", "id": "fc_1", "call_id": "different-id", "name": "Write", "arguments": `{}`}}))
	inputs = append(inputs, responsesEvent("response.output_text.delta", map[string]any{"output_index": 1, "delta": "late-start"})+responsesEvent("response.output_item.done", map[string]any{"output_index": 1, "item": map[string]any{"type": "message", "content": []any{map[string]any{"type": "refusal", "refusal": "late-start"}}}})+responsesDone(nil))
	rows := []streamSourceRow{}
	for _, oauth := range []bool{false, true} {
		for _, size := range []int{1, 7, 65536} {
			for _, input := range inputs {
				rows = append(rows, streamSourceRow{Input: input, ChunkSize: size, OAuth: oauth})
			}
		}
	}
	for _, prefix := range [][]byte{{0xef, 0xbb, 0xbf}, {0xf0, 0x90, 0x41}, {0xe2, 0x82}, {0xed, 0xa0, 0x80}} {
		bytes := append(append([]byte{}, prefix...), []byte(responsesDone([]any{}))...)
		values := []int{}
		for _, b := range bytes {
			values = append(values, int(b))
		}
		rows = append(rows, streamSourceRow{Bytes: values, ChunkSize: 1})
	}
	data := responsesSource(t, "stream.fixture.ts", map[string]any{"rows": rows})
	var source struct {
		Rows []struct {
			Frames, Terminals []string
			Error             *struct{ Name, Message, Code string }
			Collected         json.RawMessage
			CollectError      *struct {
				Name, Message, Type, Code string
				Status                    int
			}
		}
	}
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err, string(data))
	}
	for i, row := range rows {
		terminals := []string{}
		p := NewResponsesProcessor("fixture-model", ResponsesStreamOptions{OpenAICodexOAuth: row.OAuth, NowMillis: func() int64 { return 1720000000123 }, OnTerminal: func(event string) { terminals = append(terminals, event) }})
		input := []byte(row.Input)
		if row.Bytes != nil {
			input = nil
			for _, b := range row.Bytes {
				input = append(input, byte(b))
			}
		}
		frames := []string{}
		var failure error
		terminal := false
		for start := 0; start < len(input) && !terminal; start += row.ChunkSize {
			next, done, err := p.Feed(input[start:min(start+row.ChunkSize, len(input))])
			for _, frame := range next {
				frames = append(frames, string(frame))
			}
			terminal = done
			if err != nil {
				failure = err
				break
			}
		}
		if failure == nil {
			next, err := p.Finish()
			for _, frame := range next {
				frames = append(frames, string(frame))
			}
			failure = err
		}
		if failure != nil {
			if source.Rows[i].Error == nil {
				t.Errorf("case%d Goerr%s Source success", i, failure)
				continue
			}
			expected := source.Rows[i].Error
			actual := failure.(*ResponseError)
			if actual.Message != expected.Message || actual.Code != expected.Code || actual.ErrorName() != expected.Name {
				t.Errorf("case%d Go%+v Source%+v", i, actual, expected)
			}
			if strings.Contains(strings.Join(frames, ""), "event: message_stop") {
				t.Error(i, "error produced successful terminal")
			}
		} else if source.Rows[i].Error != nil {
			t.Errorf("case%d Go success Source%+v", i, source.Rows[i].Error)
		} else if !reflect.DeepEqual(frames, source.Rows[i].Frames) {
			t.Errorf("case%d\nGo%q\nSource%q", i, frames, source.Rows[i].Frames)
		}
		if !reflect.DeepEqual(terminals, source.Rows[i].Terminals) {
			t.Error(i, "terminal callbacks", terminals, source.Rows[i].Terminals)
		}
		collected, err := CollectResponses(context.Background(), io.NopCloser(strings.NewReader(string(input))), "fixture-model", ResponsesReaderOptions{ResponsesStreamOptions: ResponsesStreamOptions{OpenAICodexOAuth: row.OAuth, NowMillis: func() int64 { return 1720000000123 }}})
		if err != nil {
			expected := source.Rows[i].CollectError
			if expected == nil {
				t.Errorf("case%d collected Goerr%s Source success", i, err)
				continue
			}
			actual, ok := err.(*ResponseError)
			if !ok {
				t.Fatal(err)
			}
			if actual.Message != expected.Message || actual.ErrorName() != expected.Name || actual.Type != expected.Type || actual.Code != expected.Code || actual.Status != expected.Status {
				t.Errorf("case%d collect Go%+v Source%+v", i, actual, expected)
			}
		} else {
			if source.Rows[i].CollectError != nil {
				t.Errorf("case%d collect Go success%s Sourceerr%+v", i, responseJSON(collected), source.Rows[i].CollectError)
				continue
			}
			a := responseJSON(collected)
			b, _ := protocol.ParseAndStringifyJavaScriptJSON(source.Rows[i].Collected)
			if !bytes.Equal(a, b) {
				t.Errorf("case%d collect Go%s Source%s", i, a, b)
			}
		}
	}
}
