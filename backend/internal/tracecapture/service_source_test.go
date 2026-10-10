package tracecapture

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func fixtureCall(id, model string, status string) protocol.JSONObject {
	return obj("id", id, "sessionId", "s", "source", "anthropic", "model", model, "startedAt", "2026-10-01T01:00:00.000Z", "completedAt", "2026-10-01T01:00:01.000Z", "durationMs", 1200, "status", status, "querySource", "repl_main_thread", "request", obj("method", "POST", "url", "https://example.invalid/path?api_key=fake&space=a%20b&input_tokens=12#frag", "headers", obj("Authorization", "Bearer fake-secret-token", "X-Session-Affinity", "private", "Accept", "application/json"), "body", obj("model", model, "messages", []any{obj("role", "user", "content", "test")}, "secret", "fake", "max_tokens", 120)), "response", obj("status", 200, "headers", obj("content-type", "application/json"), "body", obj("usage", obj("input_tokens", 12, "output_tokens", 3, "cache_read_input_tokens", 4), "content", []any{obj("type", "text", "text", "done")})), "metadata", obj("phase", "api_call_completed", "input_tokens", 12, "token", "fake"))
}
func normalizePrivate(v json.RawMessage, h string) json.RawMessage {
	return json.RawMessage(strings.ReplaceAll(string(v), h, "$HOME"))
}
func TestTraceServiceActualFilesAgainstSource(t *testing.T) {
	ctx := context.Background()
	first := fixtureCall("call-a", "model-A", "ok")
	second := fixtureCall("call-b", "model-B", "error")
	second.Set("startedAt", jstr("2026-10-01T01:00:02.000Z"))
	second.Set("completedAt", jstr("2026-10-01T01:00:03.000Z"))
	updated := first.Clone()
	updated.Set("status", jstr("error"))
	updated.Set("error", jstr("Bearer fakeFailed token"))
	updated.Set("durationMs", raw(1500))
	event := obj("id", "event-a", "sessionId", "s", "timestamp", "2026-10-01T01:00:02.000Z", "phase", "api_call_completed", "callId", "call-a", "title", "Complete", "message", "sk-fake8letters12345", "metadata", obj("apiKey", "fake", "input_tokens", 12))
	tail := raw(obj("type", "event", "event", obj("id", "tail", "sessionId", "s", "timestamp", "2026-10-01T01:00:04.000Z", "phase", "pending", "severity", "info")))
	ops := []protocol.JSONObject{obj("method", "settings", "input", obj("enabled", true)), obj("method", "settingsFile"), obj("method", "call", "input", first), obj("method", "event", "input", event), obj("method", "call", "input", second), obj("method", "full", "id", "s"), obj("method", "overview", "id", "s"), obj("method", "detail", "id", "s", "callId", "call-a"), obj("method", "detail", "id", "s", "callId", "missing"), obj("method", "call", "input", updated), obj("method", "overview", "id", "s", "options", obj("offset", 1, "limit", 1)), obj("method", "files"), obj("method", "list"), obj("method", "revision", "id", "s"), obj("method", "overview", "id", "missing"), obj("method", "revision", "id", "missing", "since", 3), obj("append", string(tail), "id", "s", "method", "overview"), obj("method", "full", "id", "s"), obj("append", "\n", "id", "s", "method", "overview"), obj("method", "settings", "input", obj("enabled", false)), obj("method", "call", "input", first), obj("method", "settings", "input", obj("enabled", true)), obj("method", "delete", "id", "s"), obj("method", "delete", "id", "s"), obj("method", "list"), obj("method", "revision", "id", "s"), obj("method", "overview", "id", "s", "options", obj("scanCursor", "badcursor"))}
	for _, mode := range []string{"on", "shadow", "off"} {
		t.Run(mode, func(t *testing.T) {
			expected := array(source(t, obj("action", "service", "mode", mode, "operations", ops)).Get("observations"))
			h := fixtureHome(t)
			s, e := New(Options{Scope: filepath.Join(h, ".claude"), ResolveMode: func() string { return mode }})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close(ctx)
			for n, row := range ops {
				p := s.filePath(text(row.Get("id")))
				if row.Get("append") != nil {
					f, e := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
					if e != nil {
						t.Fatal(e)
					}
					_, e = f.WriteString(text(row.Get("append")))
					_ = f.Close()
					if e != nil {
						t.Fatal(e)
					}
				}
				id := text(row.Get("id"))
				options := object(row.Get("options"))
				var value any
				var e error
				switch text(row.Get("method")) {
				case "settings":
					var enabled *bool
					if v := object(row.Get("input")).Get("enabled"); len(v) > 0 {
						var b bool
						_ = json.Unmarshal(v, &b)
						enabled = &b
					}
					value, e = s.UpdateSettings(ctx, enabled)
				case "settingsFile":
					value = s.readSettings()
				case "call":
					v, err := s.RecordCall(ctx, object(row.Get("input")))
					e = err
					if v.IsObject() {
						value = v
					}
				case "event":
					v, err := s.RecordEvent(ctx, object(row.Get("input")))
					e = err
					if v.IsObject() {
						value = v
					}
				case "full":
					value, e = s.Full(ctx, id)
				case "overview":
					value, e = s.Overview(ctx, id, OverviewOptions{Offset: int64(number(options.Get("offset"))), Limit: int64(number(options.Get("limit"))), ScanCursor: text(options.Get("scanCursor"))})
				case "detail":
					v, err := s.Call(ctx, id, text(row.Get("callId")), "")
					e = err
					if v.IsObject() {
						value = v
					}
				case "files":
					value, e = s.ListFiles(ctx)
				case "list":
					value, e = s.List(ctx, ListOptions{})
				case "delete":
					value, e = s.Delete(ctx, id)
				case "revision":
					var since *int64
					if row.Get("since") != nil {
						since = ptr(int64(number(row.Get("since"))))
					}
					value, e = s.Revision(ctx, id, since, nil)
				}
				out := obj("value", value)
				if e != nil {
					code := json.RawMessage("null")
					if f, ok := e.(*Error); ok {
						code = jstr(f.Code)
					}
					out = obj("error", e.Error(), "code", code)
				}
				t.Run(integerString(int64(n)), func(t *testing.T) {
					compare(t, reduceObservations(expected[n]), reduceObservations(normalizePrivate(raw(out), h)))
				})
			}
			if e = s.Drain(ctx); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestTraceSettingsPreserveOriginalUnknownAndSerializeSharedWriters(t *testing.T) {
	ctx := context.Background()
	h := fixtureHome(t)
	s, e := New(Options{Scope: filepath.Join(h, ".claude")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(ctx)
	p := s.managedPath()
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	original := `{"env":{"PRIVATE":"fake"},"unknown":{"nested":[1,true]},"traceCapture":{"enabled":true,"unknown":"keep","future":{"x":1}}}`
	if e = os.WriteFile(p, []byte(original), 0600); e != nil {
		t.Fatal(e)
	}
	enabled := false
	if _, e = s.UpdateSettings(ctx, &enabled); e != nil {
		t.Fatal(e)
	}
	o := s.readSettings()
	if text(object(o.Get("env")).Get("PRIVATE")) != "fake" || text(object(o.Get("traceCapture")).Get("unknown")) != "keep" || truthy(object(o.Get("traceCapture")).Get("enabled")) {
		t.Fatalf("Unknown settings lost: %s", raw(o))
	}
}
