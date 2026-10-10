package application

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"golang.org/x/net/websocket"
)

func TestActualDesktopProviderProxyThreeProtocolsWSWriteAndScopedCredentials(t *testing.T) {
	for _, format := range []string{"anthropic", "openai_chat", "openai_responses"} {
		t.Run(format, func(t *testing.T) {
			var calls atomic.Int32
			var secondRequest atomic.Value
			var file string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer selected-key" {
					t.Errorf("selected provider was replaced by active provider: %q", r.Header.Get("Authorization"))
				}
				request, _ := io.ReadAll(r.Body)
				if !bytes.Contains(request, []byte("fixture-model")) {
					t.Errorf("wrong translated request %s", request)
				}
				n := calls.Add(1)
				if n == 2 {
					secondRequest.Store(append([]byte(nil), request...))
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if format == "anthropic" {
					if n == 1 {
						response(w, "proxy-write", "Write", map[string]string{"file_path": file, "content": "PROXY_FACTORY_FILE\n"}, "")
					} else {
						response(w, "proxy-done", "", nil, "PROXY_FACTORY_OK")
					}
					return
				}
				if format == "openai_chat" {
					if n == 1 {
						fmt.Fprintf(w, "data: %s\n\n", raw(map[string]any{"id": "proxy-write", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "proxy-write-call", "type": "function", "function": map[string]any{"name": "Write", "arguments": string(raw(map[string]string{"file_path": file, "content": "PROXY_FACTORY_FILE\n"}))}}}}, "finish_reason": "tool_calls"}}}))
					} else {
						fmt.Fprintf(w, "data: %s\n\n", raw(map[string]any{"id": "proxy-done", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": "PROXY_FACTORY_OK"}, "finish_reason": "stop"}}}))
					}
					fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw(map[string]any{"choices": []any{}, "usage": map[string]int{"prompt_tokens": 12, "completion_tokens": 8}}))
					return
				}
				var output []any
				if n == 1 {
					output = []any{map[string]any{"type": "function_call", "id": "proxy-write-item", "call_id": "proxy-write-call", "name": "Write", "arguments": string(raw(map[string]string{"file_path": file, "content": "PROXY_FACTORY_FILE\n"}))}}
				} else {
					output = []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": "PROXY_FACTORY_OK"}}}}
				}
				fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", raw(map[string]any{"type": "response.completed", "response": map[string]any{"id": "proxy-response", "status": "completed", "output": output, "usage": map[string]int{"input_tokens": 12, "output_tokens": 8}}}))
			}))
			defer upstream.Close()
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			file = filepath.Join(o.WorkDir, "proxy-factory.txt")
			config := o.Environment["CLAUDE_CONFIG_DIR"]
			o.Environment["DISABLE_TELEMETRY"] = "1"
			if err := os.MkdirAll(config, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"network":{"proxy":{"mode":"direct"}},"traceCapture":{"enabled":true}}`), 0600); err != nil {
				t.Fatal(err)
			}
			host := httptest.NewUnstartedServer(nil)
			port := host.Listener.Addr().(*net.TCPAddr).Port
			s, err := NewDesktopServer(ServerOptions{Port: port, Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
				return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			host.Config.Handler = s
			host.Start()
			defer host.Close()
			request := func(method, path string, body any, status int) protocol.JSONObject {
				t.Helper()
				r, _ := http.NewRequest(method, host.URL+path, bytes.NewReader(raw(body)))
				r.Header.Set("Content-Type", "application/json")
				res, e := host.Client().Do(r)
				if e != nil {
					t.Fatal(e)
				}
				defer res.Body.Close()
				data, e := io.ReadAll(res.Body)
				if e != nil || res.StatusCode != status {
					t.Fatalf("%s %s status%d %s %v", method, path, res.StatusCode, data, e)
				}
				return obj(data)
			}
			create := func(name, key string) string {
				return obj(request("POST", "/api/providers", map[string]any{"presetId": "custom", "name": name, "apiKey": key, "baseUrl": upstream.URL, "apiFormat": format, "authStrategy": "auth_token", "supportsNestedToolResultMedia": false, "models": map[string]string{"main": "fixture-model", "haiku": "", "sonnet": "", "opus": ""}}, 201).Get("provider")).String("id")
			}
			// The ordinary desktop setting must capture proxy calls even when
			// the parent server has no desktop entry-point/trace override env.
			request("PUT", "/api/traces/settings", map[string]bool{"enabled": true}, 200)
			selected := create("Selected", "selected-key")
			active := create("Other active", "must-not-replace-selected")
			request("POST", "/api/providers/"+active+"/activate", map[string]any{}, 200)
			if string(request("GET", "/api/providers/auth-status", nil, 200).Get("hasAuth")) != "true" {
				t.Fatal("proxy provider activation/auth did not bind")
			}
			request("GET", "/api/models", nil, 200)
			created := request("POST", "/api/sessions", map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"}, 201)
			id := created.String("sessionId")
			ws, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			_ = ws.SetDeadline(time.Now().Add(25 * time.Second))
			if err = websocket.Message.Send(ws, string(raw(map[string]any{"type": "set_runtime_config", "providerId": selected, "modelId": "fixture-model"}))); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 100; i++ {
				var line string
				if err = websocket.Message.Receive(ws, &line); err != nil {
					t.Fatal(err)
				}
				value := obj([]byte(line))
				if value.String("type") == "error" {
					t.Fatal(line)
				}
				if value.String("type") == "runtime_config_applied" {
					break
				}
				if i == 99 {
					t.Fatal("runtime selection not applied")
				}
			}
			if err = websocket.Message.Send(ws, string(raw(map[string]string{"type": "user_message", "content": "Use Write to create the private task file, then finish."}))); err != nil {
				t.Fatal(err)
			}
			finished, write := false, false
			for i := 0; i < 250; i++ {
				var line string
				if err = websocket.Message.Receive(ws, &line); err != nil {
					t.Fatal(err)
				}
				value := obj([]byte(line))
				if value.String("type") == "error" {
					t.Fatal(line)
				}
				write = write || bytes.Contains([]byte(line), []byte(`"name":"Write"`))
				finished = finished || value.String("type") == "message_complete"
				if finished && value.String("type") == "session_state" && value.String("turnState") == "idle" {
					break
				}
			}
			data, err := os.ReadFile(file)
			if err != nil || string(data) != "PROXY_FACTORY_FILE\n" || calls.Load() != 2 || !finished {
				var history any
				if v := secondRequest.Load(); v != nil {
					parsed := obj(v.([]byte))
					history = map[string]any{"messages": parsed.Get("messages"), "input": parsed.Get("input")}
				}
				t.Fatalf("actual proxy/task %q %v calls%d Write%v finish%v history%s", data, err, calls.Load(), write, finished, raw(history))
			}
			if err = s.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			trace, err := os.ReadFile(filepath.Join(config, "cc-haha", "traces", id+".jsonl"))
			if err != nil || !bytes.Contains(trace, []byte(`"source":"proxy"`)) || bytes.Contains(trace, []byte("selected-key")) {
				t.Fatalf("real proxy trace absent/leaked credential: %v %s", err, trace)
			}
		})
	}
}
