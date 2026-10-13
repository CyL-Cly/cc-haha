package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/coder/websocket"
)

func TestWebSocketRealSourceHandshakeAuthorizationRPCAndClose(t *testing.T) {
	for _, kind := range []string{"ws-ide", "ws", "ws-headers"} {
		t.Run(kind, func(t *testing.T) {
			bun := os.Getenv("CC_HAHA_SOURCE_BUN")
			if bun == "" {
				t.Skip("actual Source Bun required")
			}
			var mu sync.Mutex
			traces := [][]json.RawMessage{}
			headers := []http.Header{}
			closes := make(chan websocket.StatusCode, 2)
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, e := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"mcp"}})
				if e != nil {
					return
				}
				defer conn.CloseNow()
				mu.Lock()
				index := len(traces)
				traces = append(traces, nil)
				headers = append(headers, r.Header.Clone())
				mu.Unlock()
				for {
					_, data, e := conn.Read(r.Context())
					if e != nil {
						closes <- websocket.CloseStatus(e)
						return
					}
					mu.Lock()
					traces[index] = append(traces[index], append(json.RawMessage(nil), data...))
					mu.Unlock()
					var message protocol.JSONObject
					if e := message.UnmarshalJSON(data); e != nil {
						t.Error(e)
						return
					}
					if message.Get("id") == nil {
						continue
					}
					result := protocol.NewJSONObject()
					switch message.String("method") {
					case "initialize":
						result = object(field("protocolVersion", LatestProtocolVersion), field("capabilities", object(field("tools", object()))), field("serverInfo", object(field("name", "physical-ide"), field("version", "1"))))
					case "tools/list":
						result = object(field("tools", []any{map[string]any{"name": "actual_ide_echo", "description": "physical IDE echo", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"input": map[string]any{"type": "string"}}, "required": []string{"input"}}}}))
					case "tools/call":
						result = object(field("content", []any{map[string]any{"type": "text", "text": "IDE_ACTUAL_TOOL 中文 😀"}}))
					default:
						t.Error("unexpected actual IDE RPC", message.String("method"))
						return
					}
					reply := object(field("jsonrpc", "2.0"))
					reply.Set("id", message.Get("id"))
					reply.Set("result", raw(result))
					kind := websocket.MessageText
					if message.String("method") == "tools/call" {
						kind = websocket.MessageBinary
					}
					if e := conn.Write(r.Context(), kind, raw(reply)); e != nil {
						t.Error(e)
						return
					}
				}
			}))
			defer peer.Close()
			config := Config{Type: "ws-ide", URL: "ws" + strings.TrimPrefix(peer.URL, "http"), AuthToken: "private-ide-lock-token"}
			if kind != "ws-ide" {
				config.Type = "ws"
				config.Headers = map[string]string{"X-Fixture": "static"}
			}
			if kind == "ws-headers" {
				config.Headers["Authorization"] = "configured-overrides-ingress"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			options := normalizeOptions(Options{Version: "fixture", Environment: map[string]string{}, CWD: t.TempDir(), SessionIngressToken: func() string { return "private-session-ingress" }})
			sourceHome := t.TempDir()
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-ide-mcp.fixture.ts")
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = sourceHome
			command.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + sourceHome, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "BUN_OPTIONS=--no-env-file", "CC_HAHA_IDE_MCP_FIXTURE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
			command.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": sourceHome, "userAgent": options.UserAgent, "sessionIngressToken": "private-session-ingress", "config": map[string]any{"type": config.Type, "url": config.URL, "ideName": "private physical editor", "authToken": config.AuthToken, "headers": config.Headers}}))
			sourceOut, e := command.CombinedOutput()
			if e != nil {
				t.Fatal(e, string(sourceOut))
			}
			var source struct {
				Tools, Result json.RawMessage
				BinaryType    string
			}
			if e = json.Unmarshal(sourceOut, &source); e != nil {
				t.Fatal(e, string(sourceOut))
			}
			t.Logf("Actual pinned Source WebSocket binaryType %q", source.BinaryType)
			client, e := Connect(ctx, config, options)
			if e != nil {
				t.Fatal(e)
			}
			defer client.Close()
			if config.Type == "ws-ide" {
				if e = client.Notify(ctx, "ide_connected", object(field("pid", 170123))); e != nil {
					t.Fatal(e)
				}
			}
			tools, e := client.ListTools(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			result, e := client.CallTool(ctx, "actual_ide_echo", object(field("input", "原始 IDE 😀")), protocol.JSONObject{}, RequestOptions{})
			if e != nil {
				t.Fatal(e)
			}
			if e = client.Close(); e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(source.Tools), raw(tools)) || !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(source.Result), raw(result)) {
				t.Fatal("native ws-ide results differ", string(sourceOut), string(raw(tools)), string(raw(result)))
			}
			for i := 0; i < 2; i++ {
				select {
				case code := <-closes:
					if code != websocket.StatusNormalClosure {
						t.Fatal("native owned socket close differs", code)
					}
				case <-ctx.Done():
					t.Fatal("IDE socket was not closed/joined")
				}
			}
			mu.Lock()
			defer mu.Unlock()
			expectedCount := 4
			if kind == "ws-ide" {
				expectedCount = 5
			}
			if len(headers) != 2 || len(traces[0]) != expectedCount || len(traces[1]) != expectedCount {
				t.Fatal("extra/missing actual RPC", traces)
			}
			for _, header := range headers {
				expectedAuth := config.AuthToken
				expectedBearer := ""
				if kind != "ws-ide" {
					expectedAuth = ""
					expectedBearer = "Bearer private-session-ingress"
				}
				if kind == "ws-headers" {
					expectedBearer = "configured-overrides-ingress"
				}
				if header.Get("X-Claude-Code-Ide-Authorization") != expectedAuth || header.Get("Authorization") != expectedBearer || header.Get("User-Agent") != options.UserAgent || header.Get("Sec-Websocket-Protocol") != "mcp" {
					t.Fatal("Source IDE auth/subprotocol mismatch", header)
				}
			}
			for i := range traces[0] {
				var a, b map[string]any
				_ = json.Unmarshal(traces[0][i], &a)
				_ = json.Unmarshal(traces[1][i], &b)
				left, _ := json.Marshal(a)
				right, _ := json.Marshal(b)
				if !bytes.Equal(left, right) {
					t.Fatal("Source MCP protocol differs", string(left), string(right))
				}
			}
		})
	}
}
