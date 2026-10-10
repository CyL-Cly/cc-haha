package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

type openAIBoundaryCase struct {
	Tokens          json.RawMessage   `json:"tokens"`
	Body            json.RawMessage   `json:"body"`
	Session         string            `json:"session"`
	Agent           string            `json:"agent"`
	TurnState       string            `json:"turnState"`
	UpstreamBody    *string           `json:"upstreamBody"`
	UpstreamStatus  int               `json:"upstreamStatus"`
	UpstreamHeaders map[string]string `json:"upstreamHeaders"`
}
type openAIBoundaryExchange struct {
	row     openAIBoundaryCase
	request any
}

func (exchange *openAIBoundaryExchange) Do(request *http.Request) (*HTTPExchangeResponse, error) {
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	var body protocol.JSONObject
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	headers := map[string]string{}
	for name, values := range request.Header {
		headers[strings.ToLower(name)] = strings.Join(values, ", ")
	}
	exchange.request = map[string]any{"endpoint": request.URL.String(), "headers": headers, "body": body}
	pairs := []HeaderPair{}
	for name, value := range exchange.row.UpstreamHeaders {
		pairs = append(pairs, HeaderPair{name, value})
	}
	original, err := NewConstructorHeaders(pairs)
	if err != nil {
		return nil, err
	}
	var source io.ReadCloser = http.NoBody
	if exchange.row.UpstreamBody != nil {
		source = io.NopCloser(strings.NewReader(*exchange.row.UpstreamBody))
	}
	return &HTTPExchangeResponse{Response: &http.Response{StatusCode: exchange.row.UpstreamStatus, Header: original.HTTPHeader(), Body: source}, OriginalHeaders: original}, nil
}
func openAISSE(name, body string) string { return "event: " + name + "\ndata: " + body + "\n\n" }

func TestOpenAIFetchWrapperAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	completed := `{"id":"response-private","model":"gpt-6-sol","status":"completed","output":[{"type":"message","id":"m-private","content":[{"type":"output_text","text":"private text"}]}],"usage":{"input_tokens":23,"output_tokens":7}}`
	tool := `{"id":"response-private","status":"completed","output":[{"type":"function_call","id":"f-private","call_id":"call-private","name":"Read","arguments":"{\"file_path\":\"private.txt\"}"}],"usage":{"input_tokens":29,"output_tokens":11}}`
	success := openAISSE("response.created", `{"response":{"id":"response-private","model":"gpt-6-sol"}}`) + openAISSE("response.output_text.delta", `{"item_id":"m-private","output_index":0,"delta":"private text"}`) + openAISSE("response.completed", `{"response":`+completed+`}`)
	toolSSE := openAISSE("response.completed", `{"response":`+tool+`}`)
	rows := []openAIBoundaryCase{}
	for _, upstream := range []struct {
		status      int
		text        *string
		contentType string
	}{
		{200, &success, "text/event-stream"}, {200, &toolSSE, "Text/Event-Stream; charset=utf-8"},
		{200, &completed, "application/json"}, {204, nil, "application/json"},
		{200, stringPtr(openAISSE("response.failed", `{"response":{"error":{"code":"content_policy","message":"private policy"}}}`)), "text/event-stream"},
		{200, stringPtr("data: [DONE]\n\n"), "text/event-stream"},
		{200, stringPtr(openAISSE("response.output_text.delta", `{"item_id":"m-private","delta":"partial"}`)), "text/event-stream"},
		{200, stringPtr("event: response.completed\ndata: {invalid\n\n"), "text/event-stream"},
		{401, stringPtr("private denied"), "text/plain"}, {429, stringPtr(strings.Repeat("X", 499) + "😀tail"), "text/plain"},
		{400, stringPtr(`{"error":{"code":"cyber_policy","message":"private coded failure"}}`), "application/json"},
		{500, stringPtr(`{"error":{"code":"content_policy_violation","message":" "}}`), "application/json"},
	} {
		for _, stream := range []bool{true, false} {
			rows = append(rows, openAIBoundaryCase{Tokens: []byte(`{"accessToken":"private-access","refreshToken":"private-refresh","expiresAt":1000000000000000,"accountId":"private-account"}`), Body: mustOpenAIJSON(map[string]any{"model": "gpt-6-sol", "max_tokens": 16, "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "private input"}}}), Session: "private-root", Agent: "private-child", UpstreamBody: upstream.text, UpstreamStatus: upstream.status, UpstreamHeaders: map[string]string{"content-type": upstream.contentType, "x-request-id": "private-request", "x-codex-turn-state": "private-routing"}})
		}
	}
	rows = append(rows, openAIBoundaryCase{Tokens: []byte(`null`), Body: rows[0].Body, Session: "private-root", UpstreamStatus: 200})
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-openai-fetch-boundary.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_OPENAI_FETCH_BOUNDARY_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(mustOpenAIJSON(map[string]any{"rows": rows}))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	raw, err := command.Output()
	if err != nil {
		t.Fatalf("actual Source fetch %v %s", err, stderr.String())
	}
	var wanted []json.RawMessage
	if err = json.Unmarshal(raw, &wanted); err != nil {
		t.Fatal(err, string(raw))
	}
	if len(wanted) != len(rows) {
		t.Fatal("Source row count", len(wanted), len(rows))
	}
	for i, row := range rows {
		exchange := &openAIBoundaryExchange{row: row}
		turn := providerproxy.NewOpenAICodexTurnState(context.Background())
		turn.Capture(row.TurnState)
		client := NewAnthropic(AnthropicConfig{APIKey: providerproxy.OpenAIOAuthDummyKey, Timeout: time.Second, HTTPExchange: exchange, DefaultHeaders: http.Header{"X-Claude-Code-Session-Id": []string{row.Session}}, OpenAICodex: &OpenAICodexConfig{Env: map[string]string{"CC_HAHA_OPENAI_REQUEST_COMPRESSION": "off"}, FreshTokens: func(context.Context) (protocol.JSONObject, error) {
			var tokens protocol.JSONObject
			_ = json.Unmarshal(row.Tokens, &tokens)
			return tokens, nil
		}, AgentID: mustOpenAIJSON(row.Agent), TurnState: turn, NowMillis: func() int64 { return 1700000000000 }}})
		result, stop, failure := client.OpenPreparedResponse(context.Background(), &MessagesRequest{Path: "/v1/messages", Body: row.Body})
		got := map[string]any{"request": exchange.request, "turnState": nil}
		if turn.Get() != "" {
			got["turnState"] = turn.Get()
		}
		if failure != nil {
			var sdkFailure *SDKStreamError
			if errors.As(failure, &sdkFailure) && sdkFailure.Cause != nil {
				failure = sdkFailure.Cause
			}
			got["error"] = failure.Error()
		} else {
			got["status"] = result.Response.StatusCode
			headers := map[string]string{}
			for _, pair := range result.OriginalHeaders.Iterator() {
				headers[pair.Name] = pair.Value
			}
			got["headers"] = headers
			body, readErr := io.ReadAll(result.Response.Body)
			if readErr != nil {
				got["readError"] = readErr.Error()
			} else {
				got["body"] = string(body)
			}
			_ = result.Response.Body.Close()
			stop()
		}
		actual := sortedOpenAIJSON(mustOpenAIJSON(got))
		want := sortedOpenAIJSON(wanted[i])
		if !bytes.Equal(actual, want) {
			t.Errorf("row%d stream=%s status%d actual%s want%s", i, string(row.Body), row.UpstreamStatus, actual, want)
		}
	}
	t.Logf("compared %d actual Source fetch response/header/turn/error profiles", len(rows))
}
func stringPtr(value string) *string { return &value }
func sortedOpenAIJSON(raw []byte) []byte {
	var value any
	_ = json.Unmarshal(raw, &value)
	normalized, _ := json.Marshal(value)
	return normalized
}
