package analytics

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func testService(t *testing.T, owner *cooperative.Owner, endpoint string, modify func(*Options)) *Service {
	t.Helper()
	root := t.TempDir()
	refs := &ownerasync.NativeReferences{}
	binding, e := refs.Bind(owner)
	if e != nil {
		t.Fatal(e)
	}
	o := Options{Owner: owner, Binding: binding, ConfigDir: root, UUID: func() string { return "private-process" }, SessionID: func(*cooperative.Scope) string { return "private-session" }, Environment: func(*cooperative.Scope) map[string]string { return map[string]string{} }, Random: func() float64 { return 0.25 }, Config: func(_ *cooperative.Scope, name string) protocol.JSONObject {
		if name == "tengu_1p_event_batch_config" {
			return protocol.NewJSONObject(field("baseUrl", endpoint), field("scheduledDelayMillis", 60000), field("maxAttempts", 1))
		}
		return protocol.NewJSONObject()
	}, Record: func(_ *cooperative.Scope, event Event) (protocol.JSONObject, error) {
		return Record(RecordInput{Event: event, ID: "private-event", Timestamp: time.UnixMilli(1000), Core: protocol.NewJSONObject(field("model", "fixture"), field("sessionId", "private-session"), field("isInteractive", "false"))})
	}, Prepare: func(_ *cooperative.Scope, url string, body []byte, _ bool) (Request, error) {
		return Request{URL: url, Headers: http.Header{"Content-Type": []string{"application/json"}, "x-api-key": []string{"fake-key"}}, Body: body, Timeout: time.Second}, nil
	}, Files: OSFiles{}, HTTP: func(ctx context.Context, request Request) (Response, error) {
		ctx, cancel := context.WithTimeout(ctx, request.Timeout)
		defer cancel()
		req, e := http.NewRequestWithContext(ctx, "POST", request.URL, strings.NewReader(string(request.Body)))
		if e != nil {
			return Response{}, e
		}
		req.Header = request.Headers.Clone()
		response, e := (&http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}).Do(req)
		if e != nil {
			return Response{}, e
		}
		defer response.Body.Close()
		body, e := io.ReadAll(response.Body)
		return Response{response.StatusCode, response.Header, body}, e
	}}
	if modify != nil {
		modify(&o)
	}
	var service *Service
	e = owner.Run(context.Background(), func(scope *cooperative.Scope) error { var e error; service, e = New(scope, o); return e })
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_ = owner.Run(context.Background(), func(scope *cooperative.Scope) error { return service.Shutdown(scope) })
	})
	return service
}

func TestActualOwnerAnalyticsFireAndForgetHTTP401FallbackAndClose(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	admitted := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	keys := []string{}
	bodies := [][]byte{}
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		keys = append(keys, r.Header.Get("x-api-key"))
		bodies = append(bodies, raw)
		n := len(keys)
		mu.Unlock()
		if n == 1 {
			close(admitted)
			<-release
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(204)
	}))
	defer sink.Close()
	service := testService(t, owner, sink.URL, nil)
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		return service.Log(scope, FileOperationEvent(FileOperation{Operation: "write", Tool: "FileWriteTool", Path: []uint16{'p'}}))
	}); e != nil {
		t.Fatal(e)
	}
	flushed := make(chan error, 1)
	go func() {
		flushed <- owner.Run(context.Background(), func(scope *cooperative.Scope) error { return service.Flush(scope) })
	}()
	select {
	case <-admitted:
	case <-time.After(3 * time.Second):
		t.Fatal("real event HTTP not admitted")
	}
	read, e := owner.Submit(context.Background(), cooperative.Read, func(*cooperative.Scope) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	if e = read.Wait(); e != nil {
		t.Fatal(e)
	}
	close(release)
	if e = <-flushed; e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 2 || keys[0] != "fake-key" || keys[1] != "" || string(bodies[0]) != string(bodies[1]) {
		t.Fatalf("original auth fallback did not preserve immutable batch: keys=%v", keys)
	}
}

func TestActualOwnerAnalyticsFailureSpoolUnknownFieldsAndForeignOwner(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer sink.Close()
	service := testService(t, owner, sink.URL, nil)
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if e := service.Log(scope, Event{Name: "private-file", Metadata: protocol.NewJSONObject(field("future", map[string]int{"nested": 17}))}); e != nil {
			return e
		}
		return service.Flush(scope)
	}); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(service.o.ConfigDir, "telemetry", "1p_failed_events.private-session.private-process.json")
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var event protocol.JSONObject
	if e = json.Unmarshal(raw, &event); e != nil {
		t.Fatal(e)
	}
	if event.String("event_type") != "ClaudeCodeInternalEvent" {
		t.Fatalf("invalid real failed event spool %s", raw)
	}
	other := cooperative.New()
	defer other.Close()
	if e = other.Run(context.Background(), func(scope *cooperative.Scope) error {
		return service.Log(scope, Event{Name: "foreign", Metadata: protocol.NewJSONObject()})
	}); e != cooperative.ErrRestricted {
		t.Fatalf("foreign owner accepted %v", e)
	}
}
