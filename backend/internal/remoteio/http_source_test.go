package remoteio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type sourceHTTPPeer struct {
	mu            sync.Mutex
	server        *httptest.Server
	requests      []protocol.JSONObject
	metadataReady chan struct{}
	metadataOnce  sync.Once
}

func newSourceHTTPPeer(t *testing.T) *sourceHTTPPeer {
	p := &sourceHTTPPeer{metadataReady: make(chan struct{})}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entry := protocol.NewJSONObject(protocol.JSONField{Name: "method", Value: quote(r.Method)}, protocol.JSONField{Name: "path", Value: quote(r.URL.RequestURI())}, protocol.JSONField{Name: "auth", Value: quote(r.Header.Get("Authorization"))}, protocol.JSONField{Name: "version", Value: quote(r.Header.Get("anthropic-version"))}, protocol.JSONField{Name: "userAgent", Value: quote(r.Header.Get("User-Agent"))})
		if r.Method != "GET" {
			var body protocol.JSONObject
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			entry.Set("body", raw(body))
		}
		p.mu.Lock()
		p.requests = append(p.requests, entry)
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/stream"):
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "id: 7\nevent: client_event\ndata: {\"event_id\":\"inbound-source-id\",\"sequence_num\":7,\"payload\":{\"type\":\"user\",\"uuid\":\"inbound-source-id\",\"message\":{\"role\":\"user\",\"content\":\"real SSE input\"}}}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/worker"):
			fmt.Fprint(w, `{"worker":{"external_metadata":{"model":"restored-fixture-model","permission_mode":"default","future":{"opaque":true}}}}`)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/internal-events"):
			if r.URL.Query().Get("subagents") == "true" {
				fmt.Fprint(w, `{"data":[{"event_id":"subagent-id","event_type":"transcript","payload":{"type":"assistant","uuid":"subagent-transcript"},"agent_id":"source-agent","is_compaction":false}]}`)
			} else if r.URL.Query().Get("cursor") == "next-source-page" {
				fmt.Fprint(w, `{"data":[{"event_id":"history-2","event_type":"transcript","payload":{"type":"assistant","uuid":"history-assistant"},"is_compaction":false}]}`)
			} else {
				fmt.Fprint(w, `{"data":[{"event_id":"history-1","event_type":"transcript","payload":{"type":"user","uuid":"history-user"},"is_compaction":true}],"next_cursor":"next-source-page"}`)
			}
		default:
			fmt.Fprint(w, `{}`)
			if r.Method == "PUT" && object(object(entry.Get("body")).Get("external_metadata")).String("permission_mode") == "plan" {
				p.metadataOnce.Do(func() { close(p.metadataReady) })
			}
		}
	}))
	t.Cleanup(p.server.Close)
	return p
}

// Request batching is a scheduling primitive. Compare ordered payloads and
// committed worker state, preserving the real HTTP methods, query cursors,
// headers, epoch and received/processing/processed order independently of
// whether the native uploader grouped adjacent writes into one POST.
func (p *sourceHTTPPeer) projection() protocol.JSONObject {
	p.mu.Lock()
	defer p.mu.Unlock()
	events, internal, delivery := []protocol.JSONObject{}, []protocol.JSONObject{}, []protocol.JSONObject{}
	worker := protocol.NewJSONObject()
	gets := []string{}
	headers := []string{}
	for _, request := range p.requests {
		path := request.String("path")
		headers = append(headers, request.String("auth")+"|"+request.String("version"))
		body := object(request.Get("body"))
		if request.String("method") == "GET" {
			gets = append(gets, path)
			continue
		}
		switch {
		case strings.HasSuffix(path, "/events/delivery"):
			var rows []protocol.JSONObject
			_ = json.Unmarshal(body.Get("updates"), &rows)
			delivery = append(delivery, rows...)
		case strings.HasSuffix(path, "/internal-events"):
			var rows []protocol.JSONObject
			_ = json.Unmarshal(body.Get("events"), &rows)
			internal = append(internal, rows...)
		case strings.HasSuffix(path, "/events"):
			var rows []protocol.JSONObject
			_ = json.Unmarshal(body.Get("events"), &rows)
			events = append(events, rows...)
		case strings.HasSuffix(path, "/worker"):
			worker = coalesce(worker, body)
		}
	}
	sort.Strings(gets)
	unique := map[string]bool{}
	for _, h := range headers {
		unique[h] = true
	}
	headers = nil
	for h := range unique {
		headers = append(headers, h)
	}
	sort.Strings(headers)
	return protocol.NewJSONObject(protocol.JSONField{Name: "events", Value: raw(events)}, protocol.JSONField{Name: "internal", Value: raw(internal)}, protocol.JSONField{Name: "delivery", Value: raw(delivery)}, protocol.JSONField{Name: "worker", Value: raw(worker)}, protocol.JSONField{Name: "gets", Value: raw(gets)}, protocol.JSONField{Name: "headers", Value: raw(headers)})
}

func TestActualSourceCCRHTTPProducerSSEDeliveryPersistencePaginationAndState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	env := map[string]string{"CLAUDE_CODE_USE_CCR_V2": "1", "CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-source-token", "CLAUDE_CODE_WORKER_EPOCH": "9"}
	messages := []protocol.JSONObject{object([]byte(`{"type":"stream_event","uuid":"stream-start","session_id":"fixture","parent_tool_use_id":null,"event":{"type":"message_start","message":{"id":"message-source"}}}`)), object([]byte(`{"type":"stream_event","uuid":"delta-a","session_id":"fixture","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"first "}}}`)), object([]byte(`{"type":"stream_event","uuid":"delta-b","session_id":"fixture","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"second"}}}`)), object([]byte(`{"type":"assistant","uuid":"complete","session_id":"fixture","parent_tool_use_id":null,"message":{"id":"message-source","role":"assistant","content":"first second"}}`))}
	payload := object([]byte(`{"type":"user","uuid":"persisted-source","message":{"role":"user","content":"persistent data"},"future":{"opaque":true}}`))
	internal := []map[string]any{{"type": "transcript", "payload": payload, "options": map[string]any{"isCompaction": true, "agentId": "source-agent"}}}
	delivery := []map[string]string{{"id": "inbound-source-id", "status": "processing"}, {"id": "inbound-source-id", "status": "processed"}}
	sourcePeer := newSourceHTTPPeer(t)
	want := runSourceFixture(t, "native-remoteio-http.fixture.ts", map[string]any{"url": sourcePeer.server.URL + "/sessions/fixture", "env": env, "messages": messages, "internal": internal, "delivery": delivery})
	sourcePeer.mu.Lock()
	ua := sourcePeer.requests[0].String("userAgent")
	sourcePeer.mu.Unlock()
	goPeer := newSourceHTTPPeer(t)
	service, err := New(ctx, Options{URL: goPeer.server.URL + "/sessions/fixture", Environment: env, UserAgent: ua})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	service.Start()
	metadata, err := service.RestoredWorkerState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	incoming, err := bufio.NewReader(service).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if err = service.WriteMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	if err = service.WriteInternalEvent(ctx, "transcript", payload, true, "source-agent"); err != nil {
		t.Fatal(err)
	}
	for _, row := range delivery {
		service.ReportDelivery(row["id"], row["status"])
	}
	service.ReportState("running", nil)
	service.ReportMetadata(object([]byte(`{"model":"restored-fixture-model","permission_mode":"plan"}`)))
	if err = service.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err = service.FlushInternalEvents(ctx); err != nil {
		t.Fatal(err)
	}
	service.ccr.delivery.mu.Lock()
	tail := service.ccr.delivery.asyncTail
	service.ccr.delivery.mu.Unlock()
	if tail != nil {
		select {
		case <-tail:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err = service.ccr.delivery.flush(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-goPeer.metadataReady:
	case <-ctx.Done():
		t.Fatal("actual metadata PUT missing")
	}
	foreground, err := service.ReadInternalEvents(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	subagents, err := service.ReadInternalEvents(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Close(); err != nil {
		t.Fatal(err)
	}
	var sourceFG, sourceSG []InternalEvent
	_ = json.Unmarshal(want.Get("foreground"), &sourceFG)
	_ = json.Unmarshal(want.Get("subagents"), &sourceSG)
	for name, pair := range map[string][2]any{"metadata": {object(want.Get("metadata")), metadata}, "incoming": {want.String("incoming"), incoming}, "foreground": {sourceFG, foreground}, "subagents": {sourceSG, subagents}, "wire": {sourcePeer.projection(), goPeer.projection()}} {
		var a, b any
		_ = json.Unmarshal(raw(pair[0]), &a)
		_ = json.Unmarshal(raw(pair[1]), &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s actual Source%s Go%s", name, raw(pair[0]), raw(pair[1]))
		}
	}
}
