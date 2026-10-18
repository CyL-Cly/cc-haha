package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestPublicSDKURLCLIRealPermissionBashAllTransportsAndProcessClose(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(root, "public-sdk-url-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	capturePublicGoCoverage(t, build)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	for _, kind := range []string{"websocket", "hybrid", "ccr", "positional", "v1"} {
		t.Run(kind, func(t *testing.T) {
			home := resumeErrorHome(t, root, "go-remote-"+kind)
			destination := filepath.Join(home, "remote-physical.txt")
			command := "printf PUBLIC_REMOTE_BASH_OK > '" + destination + "'"
			var calls, approvals atomic.Int32
			var requestMu sync.Mutex
			var lastModelRequest []byte
			model := httptest.NewServer(nativeCLIModelHTTPHandler(t, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				requestMu.Lock()
				lastModelRequest = append([]byte(nil), body...)
				requestMu.Unlock()
				if calls.Add(1) == 1 {
					jsonSchemaCLIResponse(w, "public-remote-bash-call", "Bash", map[string]string{"command": command, "description": "actual public remote Bash"}, "")
					return
				}
				jsonSchemaCLIResponse(w, "public-remote-final", "", nil, "PUBLIC_REMOTE_TASK_COMPLETE")
			}))
			defer model.Close()
			stream := make(chan map[string]any, 32)
			var connectionMu, writeMu sync.Mutex
			var connection *websocket.Conn
			var sequence atomic.Int32
			queue := func(value map[string]any) {
				if kind == "ccr" {
					stream <- value
					return
				}
				connectionMu.Lock()
				conn := connection
				connectionMu.Unlock()
				if conn == nil {
					t.Error("remote socket not connected")
					return
				}
				data, _ := json.Marshal(value)
				writeMu.Lock()
				err := conn.Write(ctx, websocket.MessageText, append(data, '\n'))
				writeMu.Unlock()
				if err != nil {
					t.Error(err)
				}
			}
			complete := make(chan struct{})
			var completeOnce sync.Once
			var internalMu sync.Mutex
			internalEntries := []map[string]any{}
			control := func(id string, request map[string]any) {
				queue(map[string]any{"type": "control_request", "request_id": id, "request": request})
			}
			onFrame := func(frame map[string]any) {
				if frame["type"] == "control_response" {
					response, _ := frame["response"].(map[string]any)
					switch response["request_id"] {
					case "public-remote-init":
						control("public-remote-plan", map[string]any{"subtype": "set_permission_mode", "mode": "plan", "ultraplan": true})
					case "public-remote-plan":
						control("public-remote-default", map[string]any{"subtype": "set_permission_mode", "mode": "default"})
					case "public-remote-default":
						if kind != "positional" {
							queue(map[string]any{"type": "user", "uuid": "public-remote-command", "message": map[string]string{"role": "user", "content": "Complete the task through real Bash after approval"}})
						}
					}
				}
				if frame["type"] == "control_request" {
					request, _ := frame["request"].(map[string]any)
					if request["subtype"] != "can_use_tool" || request["tool_name"] != "Bash" {
						t.Error("actual public permission request", frame)
						return
					}
					approvals.Add(1)
					queue(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": frame["request_id"], "response": map[string]any{"behavior": "allow", "updatedInput": map[string]string{"command": command, "description": "actual public remote Bash"}}}})
				}
				if frame["type"] == "result" {
					if frame["result"] != "PUBLIC_REMOTE_TASK_COMPLETE" || frame["subtype"] != "success" {
						t.Error("public result", frame)
					}
					if kind == "ccr" {
						control("public-remote-end", map[string]any{"subtype": "end_session"})
					} else {
						connectionMu.Lock()
						conn := connection
						connectionMu.Unlock()
						_ = conn.Close(websocket.StatusCode(4001), "private retired worker")
					}
					completeOnce.Do(func() { close(complete) })
				}
			}
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fake-public-ingress" {
					t.Error("actual remote ingress auth", r.Header.Get("Authorization"))
				}
				if r.URL.Path == "/ingress/private" {
					w.Header().Set("Content-Type", "application/json")
					if r.Method == "GET" {
						json.NewEncoder(w).Encode(map[string]any{"loglines": []any{map[string]any{"type": "user", "uuid": "old-v1-user", "parentUuid": nil, "sessionId": resumeErrorInitialSID, "timestamp": "2026-10-01T00:00:00.000Z", "message": map[string]string{"role": "user", "content": "OLD_V1_REMOTE_CONTEXT"}}, map[string]any{"type": "assistant", "uuid": "old-v1-assistant", "parentUuid": "old-v1-user", "sessionId": resumeErrorInitialSID, "timestamp": "2026-10-01T00:00:01.000Z", "message": map[string]any{"role": "assistant", "content": []any{map[string]string{"type": "text", "text": "OLD_V1_ASSISTANT_CONTEXT"}}}}}})
						return
					}
					var entry map[string]any
					_ = json.NewDecoder(r.Body).Decode(&entry)
					internalMu.Lock()
					internalEntries = append(internalEntries, entry)
					internalMu.Unlock()
					w.WriteHeader(201)
					fmt.Fprint(w, `{}`)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/stream") {
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					stream <- map[string]any{"type": "control_request", "request_id": "public-remote-init", "request": map[string]string{"subtype": "initialize"}}
					for {
						select {
						case packet := <-stream:
							event := map[string]any{"event_id": fmt.Sprintf("public-source-event-%d", sequence.Add(1)), "sequence_num": sequence.Load(), "payload": packet}
							data, _ := json.Marshal(event)
							fmt.Fprintf(w, "id: %d\nevent: client_event\ndata: %s\n\n", sequence.Load(), data)
							w.(http.Flusher).Flush()
						case <-r.Context().Done():
							return
						}
					}
				}
				if strings.Contains(r.URL.Path, "/worker") {
					w.Header().Set("Content-Type", "application/json")
					if r.Method == "GET" {
						if strings.HasSuffix(r.URL.Path, "/internal-events") {
							fmt.Fprint(w, `{"data":[]}`)
						} else {
							fmt.Fprint(w, `{"worker":{"external_metadata":{}}}`)
						}
						return
					}
				}
				if r.Method == "POST" {
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					if events, ok := body["events"].([]any); ok {
						for _, row := range events {
							event := row.(map[string]any)
							if strings.HasSuffix(r.URL.Path, "/internal-events") {
								internalMu.Lock()
								internalEntries = append(internalEntries, event)
								internalMu.Unlock()
								continue
							}
							if kind == "ccr" {
								event = event["payload"].(map[string]any)
							}
							onFrame(event)
						}
					}
					fmt.Fprint(w, `{}`)
					return
				}
				if r.Method == "PUT" {
					fmt.Fprint(w, `{}`)
					return
				}
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				connectionMu.Lock()
				connection = conn
				connectionMu.Unlock()
				control("public-remote-init", map[string]any{"subtype": "initialize"})
				for {
					_, data, err := conn.Read(ctx)
					if err != nil {
						return
					}
					var frame map[string]any
					if err = json.Unmarshal(bytes.TrimSpace(data), &frame); err != nil {
						t.Error(err)
						return
					}
					onFrame(frame)
				}
			}))
			defer remote.Close()
			url := "ws" + strings.TrimPrefix(remote.URL, "http") + "/ws/private"
			env := append(resumeErrorEnv(home, root, model.URL), "CLAUDE_CODE_SESSION_ACCESS_TOKEN=fake-public-ingress")
			if kind == "hybrid" {
				env = append(env, "CLAUDE_CODE_POST_FOR_SESSION_INGRESS_V2=1")
			}
			if kind == "ccr" {
				url = remote.URL + "/sessions/private"
				env = append(env, "CLAUDE_CODE_USE_CCR_V2=1", "CLAUDE_CODE_WORKER_EPOCH=3")
			}
			if kind == "v1" {
				env = append(env, "ENABLE_SESSION_PERSISTENCE=1")
			}
			args := []string{"--sdk-url", url, "--bare", "--model", "fixture-model", "--no-computer-use", "--thinking", "disabled", "--system-prompt", "Private public remote Harness", "--tools", "Bash"}
			if kind == "ccr" {
				args = append(args, "--resume", resumeErrorInitialSID)
			}
			if kind == "v1" {
				args = append(args, "--resume", remote.URL+"/ingress/private")
			}
			if kind == "positional" {
				args = append(args, "--", "Actual remote positional prompt")
			}
			process := exec.CommandContext(ctx, binary, args...)
			process.Dir = home
			process.Env = env
			var stdout, stderr bytes.Buffer
			process.Stdout = &stdout
			process.Stderr = &stderr
			capturePublicGoCoverage(t, process)
			if err := process.Run(); err != nil {
				t.Fatal("public SDK URL process", err, "stderr", stderr.String(), "stdout", stdout.String())
			}
			select {
			case <-complete:
			default:
				t.Fatal("public remote task had no actual successful result", stderr.String())
			}
			content, err := os.ReadFile(destination)
			if err != nil || string(content) != "PUBLIC_REMOTE_BASH_OK" || calls.Load() != 2 || approvals.Load() != 1 {
				t.Fatal("actual public tool proof", err, string(content), calls.Load(), approvals.Load(), stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatal("nonbridge remote data leaked to local stdout", stdout.String())
			}
			requestMu.Lock()
			request := string(lastModelRequest)
			requestMu.Unlock()
			if !strings.Contains(request, "public-remote-bash-call") || !strings.Contains(request, "tool_result") {
				t.Fatal("actual next model did not consume Bash tool result", request)
			}
			if kind == "ccr" {
				internalMu.Lock()
				count := len(internalEntries)
				internalMu.Unlock()
				if count < 3 {
					t.Fatal("CCR public transcript consumer missing", count)
				}
			}
			if kind == "v1" {
				internalMu.Lock()
				entries := append([]map[string]any(nil), internalEntries...)
				internalMu.Unlock()
				if len(entries) < 3 || !strings.Contains(request, "OLD_V1_REMOTE_CONTEXT") || !strings.Contains(request, "OLD_V1_ASSISTANT_CONTEXT") {
					t.Fatal("public v1 remote hydration/append consumer missing", len(entries), request)
				}
				for _, entry := range entries {
					if entry["uuid"] == "old-v1-user" || entry["uuid"] == "old-v1-assistant" {
						t.Error("restored v1 entries republished", entry)
					}
				}
			}
		})
	}
}
