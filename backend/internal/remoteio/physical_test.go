package remoteio

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/coder/websocket"
)

func TestPhysicalWebSocketReconnectBufferRefreshPermanentCloseAndJoin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var connections atomic.Int32
	var mu sync.Mutex
	headers := []string{}
	received := []protocol.JSONObject{}
	first := make(chan struct{})
	release := make(chan struct{})
	complete := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		index := connections.Add(1)
		mu.Lock()
		headers = append(headers, r.Header.Get("Authorization"))
		mu.Unlock()
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		received = append(received, object(data))
		mu.Unlock()
		if index == 1 {
			close(first)
			select {
			case <-release:
			case <-ctx.Done():
				return
			}
			_ = conn.Close(websocket.StatusGoingAway, "private transient socket")
		} else {
			_ = conn.Write(ctx, websocket.MessageText, []byte("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"after reconnect\"}}\n"))
			_ = conn.Close(websocket.StatusCode(4001), "private retired session")
			close(complete)
		}
	}))
	defer peer.Close()
	service, err := New(ctx, Options{URL: "ws" + strings.TrimPrefix(peer.URL, "http"), Environment: map[string]string{"CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-first"}, Wait: func(ctx context.Context, delay time.Duration) error { return wait(ctx, time.Millisecond) }})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	message := object([]byte(`{"type":"result","uuid":"actual-buffered-id","result":"actual buffered output"}`))
	if err := service.WriteMessage(message); err != nil {
		t.Fatal(err)
	}
	service.Start()
	select {
	case <-first:
	case <-ctx.Done():
		t.Fatal("first socket did not receive buffered actual output")
	}
	service.UpdateEnvironment(map[string]string{"CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-refreshed"})
	close(release)
	data, err := io.ReadAll(service)
	if err != nil || !strings.Contains(string(data), "after reconnect") {
		t.Fatal(err, string(data))
	}
	select {
	case <-complete:
	case <-ctx.Done():
		t.Fatal("permanent socket close did not join")
	}
	if service.CloseCode() != 4001 || connections.Load() != 2 {
		t.Fatal("permanent close retried", service.CloseCode(), connections.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 || received[0].String("uuid") != "actual-buffered-id" || received[1].String("uuid") != "actual-buffered-id" || strings.Join(headers, ",") != "Bearer fake-first,Bearer fake-refreshed" {
		t.Fatal(headers, string(raw(received)))
	}
}

func TestPhysicalCCRRegistrationEarlyAckOrderedInternalFlushReadStateAndEpoch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var mu sync.Mutex
	var paths []string
	var bodies = map[string][]protocol.JSONObject{}
	var streamRequests atomic.Int32
	var eventAttempts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-ccr-token" || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Error("actual CCR auth/version", r.Header)
		}
		path := r.URL.Path
		if strings.HasSuffix(path, "/events/stream") {
			streamRequests.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "id: 1\nevent: client_event\ndata: {\"event_id\":\"command-1\",\"payload\":{\"type\":\"user\",\"uuid\":\"command-1\",\"message\":{\"role\":\"user\",\"content\":\"actual remote ASK\"}}}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		data, _ := io.ReadAll(r.Body)
		body := object(data)
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		bodies[path] = append(bodies[path], body)
		mu.Unlock()
		if r.Method == http.MethodGet {
			if strings.HasSuffix(path, "/worker") {
				_, _ = io.WriteString(w, `{"worker":{"external_metadata":{"model":"restored-model","permissionMode":"default"}}}`)
				return
			}
			if r.URL.Query().Get("cursor") == "" {
				_, _ = io.WriteString(w, `{"data":[{"event_id":"old-entry","event_type":"transcript","payload":{"type":"user","uuid":"old-id","message":{"role":"user","content":"OLD_REMOTE_CONTEXT"}},"is_compaction":false}],"next_cursor":"page-two"}`)
				return
			}
			_, _ = io.WriteString(w, `{"data":[{"event_id":"old-agent-entry","event_type":"transcript","agent_id":"original-agent","payload":{"type":"assistant","uuid":"old-agent-id","message":{"role":"assistant","content":"OLD_AGENT_CONTEXT"}},"is_compaction":false}]}`)
			return
		}
		if string(body.Get("worker_epoch")) != "7" {
			t.Error("early received acknowledgement lost epoch", string(data))
		}
		if strings.HasSuffix(path, "/events") && eventAttempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		if strings.HasSuffix(path, "/worker") && body.String("worker_status") == "requires_action" {
			w.WriteHeader(409)
			return
		}
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer peer.Close()
	service, err := New(ctx, Options{URL: peer.URL + "/v1/code/sessions/actual", Environment: map[string]string{"CLAUDE_CODE_USE_CCR_V2": "1", "CLAUDE_CODE_WORKER_EPOCH": "7", "CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-ccr-token"}, UserAgent: "claude-code/source-fixture", Wait: func(ctx context.Context, delay time.Duration) error { return wait(ctx, time.Millisecond) }})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	service.Start()
	line, err := bufio.NewReader(service).ReadString('\n')
	if err != nil || !strings.Contains(line, "actual remote ASK") {
		t.Fatal(err, line)
	}
	restored, err := service.RestoredWorkerState(ctx)
	if err != nil || restored.String("model") != "restored-model" {
		t.Fatal(err, string(raw(restored)))
	}
	if err := service.WriteMessage(object([]byte(`{"type":"result","uuid":"actual-result","result":"remote result"}`))); err != nil {
		t.Fatal(err)
	}
	if err := service.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if eventAttempts.Load() != 2 {
		t.Fatal("CCR retry lost result", eventAttempts.Load())
	}
	entry := object([]byte(`{"type":"user","uuid":"actual-transcript-id","message":{"role":"user","content":"REAL_PERSISTED_INPUT"}}`))
	if err := service.WriteInternalEvent(ctx, "transcript", entry, true, "worker-alpha"); err != nil {
		t.Fatal(err)
	}
	if err := service.FlushInternalEvents(ctx); err != nil {
		t.Fatal(err)
	}
	events, err := service.ReadInternalEvents(ctx, true)
	if err != nil || len(events) != 2 || events[0].Payload.String("uuid") != "old-id" || events[1].AgentID != "original-agent" {
		t.Fatal(err, string(raw(events)))
	}
	if err := service.ccr.delivery.flush(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	delivery := bodies["/v1/code/sessions/actual/worker/events/delivery"]
	internal := bodies["/v1/code/sessions/actual/worker/internal-events"]
	requests := append([]string(nil), paths...)
	mu.Unlock()
	var updates []protocol.JSONObject
	for _, batch := range delivery {
		var rows []protocol.JSONObject
		_ = json.Unmarshal(batch.Get("updates"), &rows)
		updates = append(updates, rows...)
	}
	if len(updates) != 1 || updates[0].String("event_id") != "command-1" || updates[0].String("status") != "received" {
		t.Fatal("early SSE delivery consumer missing", string(raw(delivery)))
	}
	var persisted []protocol.JSONObject
	for _, batch := range internal {
		var rows []protocol.JSONObject
		_ = json.Unmarshal(batch.Get("events"), &rows)
		persisted = append(persisted, rows...)
	}
	if len(persisted) != 1 || persisted[0].String("agent_id") != "worker-alpha" || string(persisted[0].Get("is_compaction")) != "true" || object(persisted[0].Get("payload")).String("uuid") != "actual-transcript-id" {
		t.Fatal("actual transcript consumer missing", string(raw(internal)))
	}
	if !strings.Contains(strings.Join(requests, "\n"), "cursor=page-two") || streamRequests.Load() != 1 {
		t.Fatal(requests, streamRequests.Load())
	}
	service.ReportState("requires_action", nil)
	for service.Err() == nil {
		select {
		case <-ctx.Done():
			t.Fatal("epoch mismatch did not end actual stream")
		case <-time.After(time.Millisecond):
		}
	}
	if service.Err() != errEpochMismatch {
		t.Fatal(service.Err())
	}
}
