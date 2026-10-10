package providerproxytransport

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func TestFullProxyHTTPAgainstActualHandler(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []map[string]any{}
	chat := `{"id":"fixture","choices":[{"index":0,"message":{"content":"fixture"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5}}`
	responses := `{"id":"fixture","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"fixture"}]}],"usage":{"input_tokens":4,"output_tokens":5}}`
	anthropic := `{"type":"message","id":"fixture","role":"assistant","model":"fixture","content":[{"type":"text","text":"fixture"}],"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":5}}`
	chatStream := "data: {\"id\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"fixture\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n"
	responsesStream := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"fixture\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + responses + "}\n\n"
	appendRow := func(format, body string, status int, stream bool, codec string) {
		wire := []byte(body)
		headers := map[string]string{"Content-Type": "application/json", "X-Request-ID": "fixture-request", "ETag": "fixture-etag", "Connection": "keep-alive, x-private-hop", "x-private-hop": "must-not-pass", "Retry-After": "1"}
		if stream {
			headers["Content-Type"] = "text/event-stream"
		}
		if codec != "" {
			var buffer bytes.Buffer
			var writer io.WriteCloser
			switch codec {
			case "gzip":
				writer = gzip.NewWriter(&buffer)
			case "deflate":
				writer = zlib.NewWriter(&buffer)
			case "br":
				writer = brotli.NewWriter(&buffer)
			case "zstd":
				writer, _ = zstd.NewWriter(&buffer, zstd.WithEncoderConcurrency(1))
			}
			_, _ = writer.Write(wire)
			_ = writer.Close()
			wire = buffer.Bytes()
			headers["Content-Encoding"] = codec
		}
		config := providerproxy.ProxyConfig{ID: "fixture", Name: "Fixture", APIKey: "fixture-key", APIFormat: format, AuthStrategy: "auth_token", SupportsNestedToolResultMedia: false, ModelAPIFormats: json.RawMessage("[]"), UpstreamHeaders: json.RawMessage("{}")}
		rows = append(rows, map[string]any{"config": config, "body": map[string]any{"model": "fixture[1m]", "max_tokens": 64, "stream": stream, "messages": []any{map[string]string{"role": "user", "content": "fixture"}}}, "headers": map[string]string{"Anthropic-Version": "2023-06-01", "Anthropic-Beta": "fixture-beta", "X-Caller": "passed"}, "wire": map[string]any{"status": status, "headers": headers, "bytes": base64.StdEncoding.EncodeToString(wire)}})
	}
	for _, format := range []string{"anthropic", "openai_chat", "openai_responses"} {
		valid := map[string]string{"anthropic": anthropic, "openai_chat": chat, "openai_responses": responses}[format]
		for _, codec := range []string{"", "gzip", "deflate", "br", "zstd"} {
			appendRow(format, valid, 200, false, codec)
		}
		appendRow(format, `{"error":{"type":"authentication_error","message":"fixture failure"}}`, 401, false, "")
		appendRow(format, `{"error":{"code":"content_policy_violation","message":"fixture rejected"}}`, 400, false, "")
		appendRow(format, strings.Repeat("a", 498)+"😀"+strings.Repeat("tail", 50), 429, false, "")
		appendRow(format, `{"error":{"code":"cyber_policy","message":"fixture rejected"}}`, 200, false, "")
		stream := map[string]string{"anthropic": "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", "openai_chat": chatStream, "openai_responses": responsesStream}[format]
		for _, codec := range []string{"", "gzip", "deflate", "br", "zstd"} {
			appendRow(format, stream, 200, true, codec)
		}
		appendRow(format, `{"unfinished":`, 200, false, "")
		appendRow(format, valid, 503, true, "")
	}
	for _, body := range []string{chatStream[:strings.Index(chatStream, "data: [DONE]")], "data: {bad}\n\n", `data: {"error":{"code":"cyber_policy","message":"rejected"}}` + "\n\n"} {
		appendRow("openai_chat", body, 200, true, "")
	}
	for _, body := range []string{"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"fixture\",\"output\":[]}}\n\n", "event: error\ndata: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"fixture error\"}\n\n"} {
		appendRow("openai_responses", body, 200, true, "")
	}
	for _, change := range []map[string]any{{"method": "GET"}, {"path": "/proxy/nope"}, {"rawBody": "{bad"}, {"noProvider": true}} {
		appendRow("openai_chat", chat, 200, false, "")
		for k, v := range change {
			rows[len(rows)-1][k] = v
		}
	}
	for _, format := range []string{"anthropic", "openai_chat", "openai_responses"} {
		for _, invalid := range []string{"null", "[]", `"fixture"`, "true", "{}", `{"model":null}`, `{"model":17}`, `{"model":{}}`} {
			appendRow(format, chat, 200, false, "")
			rows[len(rows)-1]["rawBody"] = invalid
		}
		for _, status := range []int{204, 205} {
			appendRow(format, "", status, true, "")
		}
	}
	home := t.TempDir()
	fixturePath, _ := filepath.Abs("../../../../scripts/quality-gate/go-parity/native-providerproxy-http.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=test", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	cmd.Stdin = bytes.NewReader(encoded(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Actual handler %v %s", err, stderr.String())
	}
	var expected []struct {
		Captured *struct{ Headers map[string]string }
		Status   int
		Headers  map[string]string
		Bytes    string
		Thrown   *struct{ Name, Message string }
	}
	if json.Unmarshal(output, &expected) != nil || len(expected) != len(rows) {
		t.Fatal(string(output), stderr.String())
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			wire := row["wire"].(map[string]any)
			var sentHeaders http.Header
			payload, _ := base64.StdEncoding.DecodeString(wire["bytes"].(string))
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sentHeaders = r.Header.Clone()
				_, _ = io.Copy(io.Discard, r.Body)
				for k, v := range wire["headers"].(map[string]string) {
					w.Header().Set(k, v)
				}
				w.WriteHeader(wire["status"].(int))
				_, _ = w.Write(payload)
			}))
			defer upstream.Close()
			config := row["config"].(providerproxy.ProxyConfig)
			config.BaseURL = upstream.URL
			service, err := New(Options{Version: "proxy-fixture", Provider: func(context.Context, string) (*providerproxy.ProxyConfig, error) {
				if row["noProvider"] == true {
					return nil, nil
				}
				return &config, nil
			}, Settings: func(context.Context) (protocol.JSONObject, error) {
				return object(json.RawMessage(`{"network":{"proxy":{"mode":"direct"}}}`)), nil
			}, Environment: func() map[string]string { return map[string]string{} }, NowMillis: func() int64 { return 1777000000123 }})
			if err != nil {
				t.Fatal(err)
			}
			method := "POST"
			if v, ok := row["method"].(string); ok {
				method = v
			}
			path := "/proxy/v1/messages"
			if v, ok := row["path"].(string); ok {
				path = v
			}
			input := encoded(row["body"])
			if v, ok := row["rawBody"].(string); ok {
				input = []byte(v)
			}
			request, _ := http.NewRequestWithContext(ctx, method, "http://127.0.0.1"+path, bytes.NewReader(input))
			for k, v := range row["headers"].(map[string]string) {
				request.Header.Set(k, v)
			}
			response, err := service.Handle(request)
			if expected[i].Thrown != nil {
				if expected[i].Captured != nil && err == nil && response != nil {
					defer response.Body.Close()
					_, err = io.ReadAll(response.Body)
				}
				if err == nil || err.Error() != expected[i].Thrown.Message || (sentHeaders == nil) != (expected[i].Captured == nil) {
					t.Fatalf("Source throws %v captured%v, Go response%v error%v upstreamHeaders%v", expected[i].Thrown, expected[i].Captured != nil, response, err, sentHeaders)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			actual, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("Go stream error %v Source thrown %v", err, expected[i].Thrown)
			}
			want, _ := base64.StdEncoding.DecodeString(expected[i].Bytes)
			if response.StatusCode != expected[i].Status || !bytes.Equal(actual, want) {
				t.Errorf("Source status%d body%q thrown%v Go status%d body%q", expected[i].Status, want, expected[i].Thrown, response.StatusCode, actual)
			}
			if expected[i].Captured != nil {
				for _, name := range []string{"accept", "accept-encoding", "user-agent", "connection", "authorization", "content-type", "anthropic-version", "anthropic-beta", "x-caller"} {
					if value := sentHeaders.Get(name); value != expected[i].Captured.Headers[name] {
						t.Errorf("upstream header%s Source%q Go%q", name, expected[i].Captured.Headers[name], value)
					}
				}
			}
			for _, name := range []string{"content-type", "cache-control", "connection", "content-encoding", "etag", "retry-after", "x-request-id", "x-private-hop"} {
				if value := response.Header.Get(name); value != expected[i].Headers[name] {
					t.Errorf("header%s Source%q Go%q", name, expected[i].Headers[name], value)
				}
			}
		})
	}
}
