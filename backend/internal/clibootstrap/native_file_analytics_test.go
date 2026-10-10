package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func fileAnalyticsMain(t *testing.T, model *httptest.Server, transport func(context.Context, analytics.Request) (analytics.Response, error)) *MainPrint {
	t.Helper()
	fixture := newNativePluginFixture(t)
	fixture.write(t, filepath.Join(fixture.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	n := fixture.n
	n.NativeProcess = true
	n.ManagedRoot = filepath.Join(n.Temp, "managed")
	_ = os.MkdirAll(n.ManagedRoot, 0700)
	n.ResolvePolicy = func() (*protocol.JSONObject, error) { value := protocol.NewJSONObject(); return &value, nil }
	for _, key := range []string{"NODE_ENV", "DISABLE_TELEMETRY", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		delete(n.Environment, key)
	}
	n.Environment["ANTHROPIC_API_KEY"] = "private-analytics-fake-key"
	n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	n.Environment["CLAUDE_CODE_ATTRIBUTION_HEADER"] = "0"
	n.Environment["ENABLE_TOOL_SEARCH"] = "false"
	n.Environment["PATH"] = "/usr/bin:/bin"
	ca := filepath.Join(n.Temp, "analytics-model-ca.pem")
	if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: model.Certificate().Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	n.Environment["NODE_EXTRA_CA_CERTS"] = ca
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "deepseek-flash", SystemPrompt: "Execute the requested file tools in this private workspace.", Tools: []string{"Read,Write,Edit"}, AllowedTools: []string{"Read", "Write", "Edit"}, MaxTurns: 12, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, AnalyticsHTTP: transport})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(m.Close)
	if e = m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	return m
}

func TestActualMainPrintDefaultAnalyticsFileToolsFireAndForgetAndFailureRecovery(t *testing.T) {
	var models atomic.Int32
	model := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		round := models.Add(1)
		var content protocol.JSONObject
		switch round {
		case 1:
			content = webObject(`{"type":"tool_use","id":"actual-write","name":"Write","input":{"file_path":"ordinary.txt","content":"first\nsecond\n"}}`)
		case 2:
			content = webObject(`{"type":"tool_use","id":"actual-read","name":"Read","input":{"file_path":"ordinary.txt"}}`)
		case 3:
			content = webObject(`{"type":"tool_use","id":"actual-edit","name":"Edit","input":{"file_path":"ordinary.txt","old_string":"second","new_string":"changed"}}`)
		case 4:
			content = webObject(`{"type":"tool_use","id":"actual-read-edited","name":"Read","input":{"file_path":"ordinary.txt"}}`)
		default:
			content = webObject(`{"type":"text","text":"ORDINARY_ANALYTICS_FILE_TOOLS_OK"}`)
		}
		_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{content}))
	}))
	defer model.Close()
	var mu sync.Mutex
	events := []protocol.JSONObject{}
	opened := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var envelope struct {
			Events []protocol.JSONObject `json:"events"`
		}
		_ = json.Unmarshal(raw, &envelope)
		mu.Lock()
		events = append(events, envelope.Events...)
		mu.Unlock()
		if requests.Add(1) == 1 {
			close(opened)
			<-release
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(204)
	}))
	defer sink.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	m := fileAnalyticsMain(t, model, func(ctx context.Context, input analytics.Request) (analytics.Response, error) {
		if input.URL != "https://api.anthropic.com/api/event_logging/batch" {
			t.Error("original event endpoint changed", input.URL)
		}
		req, e := http.NewRequestWithContext(ctx, "POST", sink.URL, bytes.NewReader(input.Body))
		if e != nil {
			return analytics.Response{}, e
		}
		req.Header = input.Headers.Clone()
		response, e := sink.Client().Do(req)
		if e != nil {
			return analytics.Response{}, e
		}
		defer response.Body.Close()
		body, e := io.ReadAll(response.Body)
		return analytics.Response{Status: response.StatusCode, Headers: response.Header, Body: body}, e
	})
	if e := m.RunTurn(context.Background(), webObject(`{"type":"user","message":{"role":"user","content":"run the ordinary file tool sequence"}}`)); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(m.options.Native.CWD, "ordinary.txt"))
	if e != nil || string(raw) != "first\nchanged\n" {
		t.Fatalf("actual file sequence failed: %v %q", e, raw)
	}
	if models.Load() != 5 {
		t.Fatal("file tools didn't traverse the same Harness", models.Load())
	}
	if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error { return m.fileAnalytics.BeginFlush(scope) }); e != nil {
		t.Fatal(e)
	}
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("actual event batch missing")
	}
	if e = m.RunTurn(context.Background(), webObject(`{"type":"user","message":{"role":"user","content":"continue while optional event HTTP is held"}}`)); e != nil {
		t.Fatal(e)
	}
	close(release)
	if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error { return m.fileAnalytics.Flush(scope) }); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	copyEvents := append([]protocol.JSONObject(nil), events...)
	mu.Unlock()
	names := []string{}
	for _, event := range copyEvents {
		var data protocol.JSONObject
		_ = json.Unmarshal(event.Get("event_data"), &data)
		names = append(names, data.String("event_name"))
		if bytes.Contains(event.Get("event_data"), []byte("ordinary.txt")) || bytes.Contains(event.Get("event_data"), []byte("changed")) {
			t.Fatal("raw file path/content in event")
		}
	}
	if !strings.Contains(strings.Join(names, ","), "tengu_file_operation") || !strings.Contains(strings.Join(names, ","), "tengu_edit_string_lengths") || !strings.Contains(strings.Join(names, ","), "tengu_session_file_read") {
		t.Fatal("actual file event producers missing", names)
	}
	if e = m.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestActualMainPrintAnalyticsShutdownCancelsAndJoinsHeldSocketWithinBudget(t *testing.T) {
	model := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"text","text":"idle"}`)}))
	}))
	defer model.Close()
	opened := make(chan struct{})
	closed := make(chan struct{})
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"pending":`)
		w.(http.Flusher).Flush()
		close(opened)
		<-r.Context().Done()
		close(closed)
	}))
	defer sink.Close()
	m := fileAnalyticsMain(t, model, func(ctx context.Context, input analytics.Request) (analytics.Response, error) {
		req, _ := http.NewRequestWithContext(ctx, "POST", sink.URL, bytes.NewReader(input.Body))
		response, e := sink.Client().Do(req)
		if e != nil {
			return analytics.Response{}, e
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(response.Body)
		return analytics.Response{Status: response.StatusCode, Body: raw}, e
	})
	if e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		return m.reportNativeFileEvent(scope, analytics.Event{Name: "tengu_file_operation", Metadata: protocol.NewJSONObject()})
	}); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	m.Close()
	elapsed := time.Since(start)
	select {
	case <-opened:
	default:
		t.Fatal("shutdown did not flush actual batch")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("500ms boundary left real socket alive")
	}
	if elapsed < 450*time.Millisecond || elapsed > 2*time.Second {
		t.Fatal("analytics exit budget", elapsed)
	}
}
