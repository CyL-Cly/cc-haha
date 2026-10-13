package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestNativeRemoteCCRRestoresActualHistoryPersistsNewMessagesAndReportsLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	const sid = "90909090-9090-4090-8090-909090909090"
	oldUser := map[string]any{"type": "user", "uuid": "old-user", "parentUuid": nil, "sessionId": sid, "timestamp": "2026-10-01T00:00:00.000Z", "message": map[string]any{"role": "user", "content": "OLD_CCR_REMOTE_CONTEXT"}}
	oldAssistant := map[string]any{"type": "assistant", "uuid": "old-assistant", "parentUuid": "old-user", "sessionId": sid, "timestamp": "2026-10-01T00:00:01.000Z", "message": map[string]any{"role": "assistant", "content": []any{map[string]string{"type": "text", "text": "OLD_CCR_ASSISTANT_CONTEXT"}}}}
	var mu sync.Mutex
	var requests []protocol.JSONObject
	var internal, deliveries []protocol.JSONObject
	events := make(chan protocol.JSONObject, 8)
	var sequence int
	push := func(id string, payload any) {
		if value, ok := payload.(map[string]any); ok {
			value["uuid"] = id
		}
		events <- nativeInProcessObject(rawMain(map[string]any{"event_id": id, "payload": payload}))
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, nativeInProcessObject(data))
		mu.Unlock()
		emitPlanFixture(w, 1, "", "NATIVE_CCR_CURRENT_RESULT")
	}))
	defer model.Close()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-native-ccr-ingress" {
			t.Error("actual CCR ingress token", r.Header.Get("Authorization"))
		}
		path := r.URL.Path
		if strings.HasSuffix(path, "/events/stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			for {
				select {
				case <-r.Context().Done():
					return
				case event := <-events:
					sequence++
					_, _ = fmt.Fprintf(w, "id: %d\nevent: client_event\ndata: %s\n\n", sequence, rawMain(event))
					w.(http.Flusher).Flush()
				}
			}
		}
		if r.Method == http.MethodGet {
			if strings.HasSuffix(path, "/worker") {
				_, _ = io.WriteString(w, `{"worker":{"external_metadata":{"model":"restored-fixture-model","permission_mode":"default"}}}`)
				return
			}
			if r.URL.Query().Get("subagents") == "true" {
				_, _ = io.WriteString(w, `{"data":[]}`)
				return
			}
			_, _ = w.Write(rawMain(map[string]any{"data": []any{map[string]any{"event_id": "old-user-event", "payload": oldUser}, map[string]any{"event_id": "old-assistant-event", "payload": oldAssistant}}}))
			return
		}
		data, _ := io.ReadAll(r.Body)
		body := nativeInProcessObject(data)
		if string(body.Get("worker_epoch")) != "4" {
			t.Error("actual epoch consumer", string(data))
		}
		if strings.HasSuffix(path, "/events/delivery") {
			var rows []protocol.JSONObject
			_ = json.Unmarshal(body.Get("updates"), &rows)
			mu.Lock()
			deliveries = append(deliveries, rows...)
			mu.Unlock()
		}
		if strings.HasSuffix(path, "/internal-events") {
			var rows []protocol.JSONObject
			_ = json.Unmarshal(body.Get("events"), &rows)
			mu.Lock()
			internal = append(internal, rows...)
			mu.Unlock()
		}
		if strings.HasSuffix(path, "/events") {
			var rows []protocol.JSONObject
			_ = json.Unmarshal(body.Get("events"), &rows)
			for _, row := range rows {
				message := nativeInProcessObject(row.Get("payload"))
				if message.String("type") == "control_response" && nativeInProcessObject(message.Get("response")).String("request_id") == "native-ccr-init" {
					push("orphan-ccr-control", map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": "old-orphan", "response": map[string]any{}}})
					push("orphan-ccr-control", map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": "old-orphan", "response": map[string]any{}}})
					push("native-ccr-inline", map[string]any{"type": "control_request", "request_id": "native-ccr-inline", "request": map[string]string{"subtype": "get_settings"}})
				}
				if message.String("type") == "control_response" && nativeInProcessObject(message.Get("response")).String("request_id") == "native-ccr-inline" {
					push("actual-ccr-command", map[string]any{"type": "user", "uuid": "actual-ccr-command", "message": map[string]string{"role": "user", "content": "Continue actual restored remote history"}})
				}
				if message.String("type") == "result" {
					if message.String("result") != "NATIVE_CCR_CURRENT_RESULT" {
						t.Error(string(rawMain(message)))
					}
					push("native-ccr-end", map[string]any{"type": "control_request", "request_id": "native-ccr-end", "request": map[string]string{"subtype": "end_session"}})
				}
			}
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-native-ccr-model"
	f.n.Environment["CLAUDE_CODE_SESSION_ACCESS_TOKEN"] = "fake-native-ccr-ingress"
	f.n.Environment["CLAUDE_CODE_WORKER_EPOCH"] = "4"
	f.n.Environment["CLAUDE_CODE_USE_CCR_V2"] = "1"
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	disabled := false
	var output, diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{PublicPrintLoadErrors: true, Arguments: PrintArguments{SDKURL: peer.URL + "/v1/code/sessions/" + sid, Resume: sid, ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual CCR resumed Harness", Tools: []string{""}, MaxTurns: 4, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: &output, Error: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	push("native-ccr-init", map[string]any{"type": "control_request", "request_id": "native-ccr-init", "request": map[string]string{"subtype": "initialize"}})
	code, err := m.Run(ctx)
	if err != nil || code != 0 {
		t.Fatal("actual CCR MainPrint failed", code, err, diagnostics.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 || requests[0].String("model") != "restored-fixture-model" || !bytes.Contains(requests[0].Get("messages"), []byte("OLD_CCR_REMOTE_CONTEXT")) || !bytes.Contains(requests[0].Get("messages"), []byte("OLD_CCR_ASSISTANT_CONTEXT")) {
		t.Fatal("actual resume history/model missing", string(rawMain(requests)))
	}
	seen := map[string]bool{}
	for _, event := range internal {
		entry := nativeInProcessObject(event.Get("payload"))
		if entry.String("sessionId") != sid {
			t.Error("transcript changed restored identity", string(rawMain(entry)))
		}
		if entry.String("uuid") == "old-user" || entry.String("uuid") == "old-assistant" {
			t.Error("restored old entries were re-published", string(rawMain(entry)))
		}
		seen[entry.String("type")] = true
	}
	if !seen["user"] || !seen["assistant"] {
		t.Fatal("original transcript append never reached CCR HTTP sink", string(rawMain(internal)))
	}
	states := map[string]bool{}
	for _, event := range deliveries {
		if event.String("event_id") == "actual-ccr-command" {
			states[event.String("status")] = true
		}
	}
	if !states["received"] || !states["processing"] || !states["processed"] {
		t.Fatal("actual command lifecycle consumer missing", string(rawMain(deliveries)))
	}
	for _, id := range []string{"native-ccr-init", "native-ccr-inline", "orphan-ccr-control"} {
		inline := []string{}
		for _, event := range deliveries {
			if event.String("event_id") == id {
				inline = append(inline, event.String("status"))
			}
		}
		joined := strings.Join(inline, ",")
		if !strings.Contains(joined, "received") || !strings.Contains(joined, "processed") || strings.Contains(joined, "processing") || id == "orphan-ccr-control" && strings.Count(joined, "processed") != 2 {
			t.Errorf("actual Source inline/orphan lifecycle missing %s: %v", id, inline)
		}
	}
	if output.Len() != 0 {
		t.Fatal("remote output leaked to local stdout", output.String())
	}
}
