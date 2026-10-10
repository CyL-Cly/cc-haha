package providerproxy

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
)

func chatChunk(delta, finish, usage string) string {
	value := `{"id":"stream-id","model":"wire-model","choices":[{"delta":` + delta
	if finish != "" {
		value += `,"finish_reason":` + finish
	}
	value += `}]`
	if usage != "" {
		value += `,"usage":` + usage
	}
	return "data: " + value + "}\n\n"
}
func TestChatSSEAgainstActualSourceFramesAndSegmentation(t *testing.T) {
	text := chatChunk(`{"content":"hello你好\ud800"}`, "", "")
	tool := chatChunk(`{"tool_calls":[{"index":0,"id":"call","function":{"name":"Read","arguments":"{\"path\":"}}]}`, "", "")
	end := chatChunk(`{}`, `"stop"`, "")
	done := "data: [DONE]\n\n"
	inputs := []string{
		end + done, text + end + done, text + end, text, text + end + "data:{broken}\n\n", text + end + chatChunk(`{"content":"late"}`, "", "") + done,
		chatChunk(`{"reasoning_content":"think","content":"text","tool_calls":[{"index":0,"id":"call","function":{"name":"Read","arguments":"{}"}}]}`, `"tool_calls"`, "") + done,
		tool + chatChunk(`{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}`, `"tool_calls"`, "") + done,
		tool + chatChunk(`{}`, `"length"`, "") + done,
		tool + chatChunk(`{}`, `"content_filter"`, "") + done,
		chatChunk(`{}`, `"tool_calls"`, "") + done,
		text + end + `data:{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":9}}}` + "\n\n" + done,
		"data:{\"error\":{\"code\":\"content_policy\",\"message\":\"policy\\ud800\"}}\n\n",
		"event:error\ndata:{}\n\n", text + "data:{\"error\":\"failure\\ud800\"}\n\n", done, "", ":keepalive\r\n\r\n" + strings.ReplaceAll(text+end+done, "\n", "\r\n"),
		"data: {\ndata: \"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]\ndata: }\n\n" + done,
		strings.TrimSuffix(end, "\n\n"), "data:null\n\n", text + end + "data:{\"choices\":[]}\n\n" + done,
		chatChunk(`{"thinking_blocks":[{"type":"thinking","signature":"signed\udc00"}]}`, `"stop"`, "") + done,
		chatChunk(`{"thinking_blocks":[null]}`, `"stop"`, "") + done,
		chatChunk(`{"content":false}`, `"stop"`, "") + done,
	}
	for _, args := range []string{`"{}"`, `" "`, `"{broken}"`, `"[]"`, `"null"`, `null`, `false`, `0`, `{"future":true,"x":"\ud800"}`} {
		inputs = append(inputs, chatChunk(`{"tool_calls":[{"index":0,"id":"call","function":{"name":"Read","arguments":`+args+`}}]}`, `"tool_calls"`, "")+done)
	}
	for _, call := range []string{`{"id":"call","function":{"name":"Read"}}`, `{"index":-1}`, `{"index":0.5}`, `{"index":"0"}`, `{"index":null}`, `{"index":1e40,"id":"call","function":{"name":"Read","arguments":"{}"}}`, `null`} {
		inputs = append(inputs, chatChunk(`{"tool_calls":[`+call+`]}`, `"stop"`, "")+done)
	}
	for _, change := range []string{`{"index":0,"id":"other"}`, `{"index":0,"function":{"name":"Write"}}`, `{"index":1,"id":"call","function":{"name":"Write"}}`} {
		inputs = append(inputs, chatChunk(`{"tool_calls":[{"index":0,"id":"call","function":{"name":"Read","arguments":"{}"}}]}`, "", "")+chatChunk(`{"tool_calls":[`+change+`]}`, `"stop"`, "")+done)
	}
	inputs = append(inputs, chatChunk(`{"tool_calls":[{"index":0,"function":{"name":"Re","arguments":"{}"}},{"index":0,"id":"c","function":{"name":"ad"}},{"index":1,"id":"c2","function":{"name":"Write","arguments":"{}"}}]}`, `"tool_calls"`, "")+done)
	for _, reason := range []string{`"future\ud800"`, `true`, `42`, `null`, `""`} {
		inputs = append(inputs, chatChunk(`{}`, reason, "")+done)
	}
	for _, usage := range []string{`{}`, `null`, `{"prompt_tokens":100,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":80}}`, `{"input_tokens":100,"output_tokens":7,"cache_read_input_tokens":20}`} {
		inputs = append(inputs, chatChunk(`{}`, `"stop"`, usage)+done)
	}
	rows := []streamSourceRow{}
	for _, size := range []int{1, 7, 65536} {
		for _, input := range inputs {
			rows = append(rows, streamSourceRow{Input: input, ChunkSize: size})
		}
	}
	for _, prefix := range [][]byte{{0xef, 0xbb, 0xbf}, {0xf0, 0x90, 0x41}, {0xe2, 0x82}, {0xed, 0xa0, 0x80}} {
		values := []int{}
		for _, b := range append(append([]byte{}, prefix...), []byte(end+done)...) {
			values = append(values, int(b))
		}
		rows = append(rows, streamSourceRow{Bytes: values, ChunkSize: 1})
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-providerproxy-chat-stream.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(requestEncode(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Chat stream Source %v %s", err, stderr.String())
	}
	var expected []struct{ Frames []string }
	if json.Unmarshal(output, &expected) != nil || len(expected) != len(rows) {
		t.Fatal(string(output))
	}
	for i, row := range rows {
		p := NewChatProcessor("fixture-model", ChatStreamOptions{NowMillis: func() int64 { return 1777000000123 }})
		input := []byte(row.Input)
		if row.Bytes != nil {
			input = nil
			for _, b := range row.Bytes {
				input = append(input, byte(b))
			}
		}
		frames := []string{}
		ended := false
		for start := 0; start < len(input) && !ended; start += row.ChunkSize {
			next, done := p.Feed(input[start:min(start+row.ChunkSize, len(input))])
			ended = done
			for _, f := range next {
				frames = append(frames, string(f))
			}
		}
		for _, f := range p.Finish() {
			frames = append(frames, string(f))
		}
		if !reflect.DeepEqual(frames, expected[i].Frames) {
			t.Errorf("case%d input%s bytes%v Source%q Go%q", i, row.Input, row.Bytes, expected[i].Frames, frames)
		}
	}
}
