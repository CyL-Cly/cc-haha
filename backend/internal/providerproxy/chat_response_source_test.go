package providerproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type chatSourceScenario struct {
	Kind    string              `json:"kind"`
	Value   json.RawMessage     `json:"value"`
	Options protocol.JSONObject `json:"options"`
}

func compareChatSource(t *testing.T, rows []chatSourceScenario, transform func(chatSourceScenario) (json.RawMessage, error)) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-providerproxy-chat.fixture.ts")
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
		t.Fatalf("actual Chat Source %v %s", err, stderr.String())
	}
	var expected []json.RawMessage
	if json.Unmarshal(output, &expected) != nil || len(expected) != len(rows) {
		t.Fatal("unexpected actual Chat Source", string(output))
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			actual, err := transform(row)
			if err != nil {
				name := "Error"
				if failure, ok := err.(*ResponseError); ok {
					name = failure.ErrorName()
				}
				message := requestEncode(err.Error())
				if failure, ok := err.(*ResponseError); ok && failure.MessageJSON != nil {
					message = failure.MessageJSON
				}
				actual = requestEncode(requestRecord(requestField("error", requestRecord(requestField("name", name), protocol.JSONField{Name: "message", Value: message}))))
			}
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(expected[i]), protocol.StringifyValidatedJavaScriptJSON(actual)) {
				t.Fatalf("Source %s Go %s", expected[i], actual)
			}
		})
	}
}

func TestChatResponseAgainstActualSourceCompletedArgumentsThinkingUsageAndFailures(t *testing.T) {
	rows := []chatSourceScenario{}
	for _, value := range []string{`null`, `false`, `0`, `[]`, `{}`, `{"error":null}`, `{"error":""}`, `{"error":"denied\ud800"}`, `{"error":{"message":"bad"}}`, `{"error":{"message":false}}`, `{"error":[]}`, `{"status":"failed"}`, `{"status":"cancelled"}`, `{"status":"incomplete"}`} {
		rows = append(rows, chatSourceScenario{Kind: "error", Value: []byte(value)})
	}
	for _, finish := range []string{"", `null`, `"stop"`, `"tool_calls"`, `"length"`, `"content_filter"`, `"future"`} {
		for _, message := range []string{`{}`, `{"content":"plain\ud800"}`, `{"content":[{"type":"text","text":"opaque"}]}`, `{"reasoning_content":"reason\udc00","reasoning":"other"}`, `{"reasoning_content":"","reasoning":"reason"}`, `{"thinking_blocks":[{"type":"thinking","thinking":"","signature":null},{"type":"thinking","thinking":"second","signature":"signed"},{"type":"unknown","thinking":"skip"}]}`, `{"tool_calls":[{"id":"call","function":{"name":"Read","arguments":"{\"path\":\"x\\ud800\",\"z\":2,\"a\":1}"}}]}`} {
			choice := requestRecord(requestField("message", requestObject([]byte(message))))
			if finish != "" {
				choice.Set("finish_reason", []byte(finish))
			}
			rows = append(rows, chatSourceScenario{Kind: "response", Value: requestEncode(requestRecord(requestField("choices", []protocol.JSONObject{choice})))})
		}
	}
	for _, arguments := range []string{"", `null`, `""`, `" "`, `"{}"`, `"{broken}"`, `"{\"x\":1e10000,\"duplicate\":1,\"duplicate\":2}"`, `"[]"`, `"false"`, `"null"`, `0`, `false`, `[]`, `{}`, `{"x":"\ud800","future":true}`} {
		for _, finish := range []string{"stop", "tool_calls", "length", "content_filter"} {
			fn := requestRecord(requestField("name", "Read"))
			if arguments != "" {
				fn.Set("arguments", []byte(arguments))
			}
			rows = append(rows, chatSourceScenario{Kind: "response", Value: requestEncode(requestRecord(requestField("choices", []protocol.JSONObject{requestRecord(requestField("finish_reason", finish), requestField("message", requestRecord(requestField("tool_calls", []protocol.JSONObject{requestRecord(requestField("id", "call"), requestField("function", fn))}))))})))})
		}
	}
	for _, calls := range []string{`[]`, `[{"id":"","function":{"name":"Read","arguments":"{}"}}]`, `[{"id":"call","function":{"name":" ","arguments":"{}"}}]`, `[{"id":"call"}]`, `[{"id":"call","function":{"name":"Read","arguments":"{}"}},{"id":"call","function":{"name":"Read","arguments":"{}"}}]`} {
		rows = append(rows, chatSourceScenario{Kind: "response", Value: []byte(`{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":` + calls + `}}]}`)})
	}
	for _, usage := range []string{`null`, `{}`, `{"prompt_tokens":100,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":80}}`, `{"input_tokens":100,"output_tokens":7,"cache_read_input_tokens":20}`, `{"total_tokens":20,"completion_tokens":5}`, `{"total_tokens":20}`, `{"input_tokens":0,"output_tokens":0}`, `{"prompt_tokens":2,"prompt_tokens_details":{"cached_tokens":9}}`} {
		rows = append(rows, chatSourceScenario{Kind: "response", Value: []byte(`{"id":"upstream\ud800","model":"wire\udc00","choices":[{"finish_reason":"stop","message":{"content":"x"}}],"usage":` + usage + `}`)})
	}
	compareChatSource(t, rows, func(row chatSourceScenario) (json.RawMessage, error) {
		if row.Kind == "error" {
			err := ChatResponseError(row.Value)
			if err != nil {
				if failure, ok := err.(*ResponseError); ok && failure.MessageJSON != nil {
					return failure.MessageJSON, nil
				}
				return requestEncode(err.Error()), nil
			}
			return []byte("null"), nil
		}
		value, err := ChatToAnthropic(requestObject(row.Value), "requested-model", func() int64 { return 1777000000123 })
		return requestEncode(value), err
	})
}
