package providerproxy

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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func responsesSource(t *testing.T, name string, input map[string]any) []byte {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	input["home"] = home
	fixture, _ := filepath.Abs("testdata/" + name)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_RESPONSES_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "TZ=UTC"}
	cmd.Stdin = bytes.NewReader(responseJSON(input))
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	return data
}

type responseSourceCase struct {
	Action    string          `json:"action"`
	Value     json.RawMessage `json:"value,omitempty"`
	Event     string          `json:"event,omitempty"`
	Reasoning bool            `json:"reasoning,omitempty"`
}

func TestActualBufferedResponsesReasoningToolsUsageTerminalAndErrorsMatchSource(t *testing.T) {
	rows := []responseSourceCase{}
	for _, reasoning := range []bool{false, true} {
		for _, value := range []string{
			`{"id":"real","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":3,"input_tokens_details":{"cached_tokens":4}}}`,
			`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"<你好>\ud800"},{"type":"refusal","refusal":""}]},{"type":"reasoning","id":"rs_1","encrypted_content":"opaque<secret>","summary":[{"type":"summary_text","text":"think"}]},{"type":"function_call","call_id":"real-call","name":"Write","arguments":"{\"text\":\"\\ud800\",\"n\":2}"}]}`,
			`{"status":"completed","output":[{"type":"reasoning","summary":[{"text":"one"},{"text":"two"},{"text":""}]}]}`,
			`{"status":"completed","output":[{"type":"function_call","call_id":"c","name":"Read","arguments":"{bad<json>\ud800"}]}`,
			`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"function_call","call_id":"c","name":"Read","arguments":"{bad"},{"type":"message","content":[{"type":"text","text":"partial"}]}]}`,
			`{"status":"failed","error":{"type":"rate_limit_error","code":"overload","message":"actual error"}}`,
			`{"status":"completed","error":{"code":"content_policy","message":" policy "}}`,
			`{"status":"completed","output":{}}`,
			`{"status":"completed","output":[{"type":"function_call","call_id":"c","name":"Read","arguments":"[]"}]}`,
			`{"status":"completed","output":[{"type":"function_call","call_id":"c","name":"Read","arguments":""}]}`,
			`{"status":"completed","output":[{"type":"message","content":[]}],"id":0,"model":false}`,
			`{"status":"completed","output":[null]}`,
			`{"status":"completed","output":[{"type":"message","content":"x"}]}`,
			`{"status":"completed","output":[{"type":"message","content":{}}]}`,
		} {
			rows = append(rows, responseSourceCase{Action: "response", Value: []byte(value), Reasoning: reasoning})
		}
	}
	for _, value := range []string{`null`, `{}`, `{"total_tokens":40}`, `{"input_tokens":-0,"output_tokens":3}`, `{"input_tokens":5,"output_tokens":3,"cache_read_input_tokens":0,"input_tokens_details":{"cached_tokens":99}}`, `{"prompt_tokens":8,"completion_tokens":3,"cache_read_input_tokens":2,"cache_creation_input_tokens":1,"prompt_tokens_details":{"cached_tokens":7}}`, `{"input_tokens":"5","output_tokens":-2,"total_tokens":4}`} {
		rows = append(rows, responseSourceCase{Action: "usage", Value: []byte(value)})
	}
	for _, value := range []string{`null`, `{}`, `""`, `"\ufeff "`, `"{bad\ud800"`, `"{\"key\\ud800\":\"\\udc00\"}"`, `"[1]"`, `"{\"n\":1e999,\"zero\":-0}"`} {
		rows = append(rows, responseSourceCase{Action: "arguments", Value: []byte(value)})
	}
	for _, value := range []string{`{}`, `{"status":""}`, `{"status":3}`, `{"status":"incomplete","incomplete_details":{"reason":""}}`, `{"status":"in_progress"}`, `{"status":"failed","error":""}`, `{"status":"failed","error":{"code":"rate_limit_exceeded"}}`, `{"status":"completed","cause":{"message":"400 {\"error\":{\"code\":\"cyber_policy\"}}"}}`} {
		for _, event := range []string{"", "response.completed", "response.failed"} {
			rows = append(rows, responseSourceCase{Action: "terminal", Value: []byte(value), Event: event})
		}
	}
	data := responsesSource(t, "response.fixture.ts", map[string]any{"rows": rows})
	var source struct {
		Rows []struct {
			Value json.RawMessage
			Error *struct {
				Name, Message, Type, Code string
				Status                    int
			}
		}
	}
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err, string(data))
	}
	for i, row := range rows {
		var actual json.RawMessage
		var err error
		switch row.Action {
		case "response":
			var value protocol.JSONObject
			value, err = ResponsesToAnthropic(responseObject(row.Value), "fixture-model", ResponsesOptions{PreserveOpenAIReasoning: row.Reasoning, NowMillis: func() int64 { return 1720000000123 }})
			actual = responseJSON(value)
		case "usage":
			actual = responseJSON(OpenAIUsageToAnthropic(row.Value))
		case "arguments":
			actual, err = ParseResponsesToolArguments(row.Value)
		case "terminal":
			var value string
			value, err = responsesTerminal(responseObject(row.Value), row.Event)
			actual = responseJSON(value)
		}
		if err != nil {
			if source.Rows[i].Error == nil {
				t.Errorf("case%d unexpected Goerr %v", i, err)
				continue
			}
			failure, ok := err.(*ResponseError)
			if !ok {
				t.Fatal(err)
			}
			expected := source.Rows[i].Error
			if failure.ErrorName() != expected.Name || failure.Message != expected.Message || failure.Type != expected.Type || failure.Code != expected.Code || failure.Status != expected.Status {
				t.Errorf("case%d Go%+v Source%+v", i, failure, expected)
			}
			continue
		}
		if source.Rows[i].Error != nil {
			t.Errorf("case%d Go success%s Source%+v", i, actual, source.Rows[i].Error)
			continue
		}
		a, e := protocol.ParseAndStringifyJavaScriptJSON(actual)
		if e != nil {
			t.Fatal(e, string(actual))
		}
		b, e := protocol.ParseAndStringifyJavaScriptJSON(source.Rows[i].Value)
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case%d Go%s Source%s", i, a, b)
		}
	}
}
