package tracecapture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func boundedRecord(id string) protocol.JSONObject {
	return obj("id", id, "sessionId", "s", "source", "anthropic", "startedAt", "2026-10-01T00:00:00.000Z", "model", "fixture", "status", "ok", "request", obj("method", "POST", "url", "https://fixture.invalid/messages", "headers", obj(), "body", EmptyBody(0)))
}
func TestBoundedWindowsAndOversizedRecordsAgainstSource(t *testing.T) {
	record := boundedRecord("unused")
	following := string(raw(obj("type", "call", "record", boundedRecord("following")))) + "\n"
	ops := []protocol.JSONObject{obj("method", "overview", "id", "s", "repeatWrite", WindowRecordLimit+5, "record", record), obj("method", "overview", "id", "s", "options", obj("offset", 9999, "limit", 1, "revisionToken", "$TOKEN")), obj("method", "overview", "id", "s", "options", obj("scanCursor", "$NEXT")), obj("method", "detail", "id", "s", "callId", "c10004"), obj("method", "overview", "id", "large", "oversizedWrite", RecordBytesLimit+32, "following", following), obj("method", "detail", "id", "large", "callId", "missing"), obj("method", "detail", "id", "large", "callId", "following"), obj("method", "full", "id", "large")}
	for _, mode := range []string{"on", "off"} {
		t.Run(mode, func(t *testing.T) {
			expected := array(source(t, obj("action", "service", "mode", mode, "operations", ops)).Get("observations"))
			h := fixtureHome(t)
			s, e := New(Options{Scope: filepath.Join(h, ".claude"), ResolveMode: func() string { return mode }})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close(context.Background())
			if e = os.MkdirAll(s.storageDir(), 0700); e != nil {
				t.Fatal(e)
			}
			var last Window
			for n, row := range ops {
				id := text(row.Get("id"))
				if numeric(row.Get("repeatWrite")) {
					f, e := os.Create(s.filePath(id))
					if e != nil {
						t.Fatal(e)
					}
					for k := 0; k < int(number(row.Get("repeatWrite"))); k++ {
						c := object(row.Get("record")).Clone()
						c.Set("id", jstr("c"+fmtIndex(k)))
						_, e = f.Write(append(raw(obj("type", "call", "record", c)), '\n'))
						if e != nil {
							t.Fatal(e)
						}
					}
					if e = f.Close(); e != nil {
						t.Fatal(e)
					}
				}
				if numeric(row.Get("oversizedWrite")) {
					b := append(raw(obj("ignored", strings.Repeat("a", int(number(row.Get("oversizedWrite")))))), '\n')
					b = append(b, []byte(text(row.Get("following")))...)
					if e = os.WriteFile(s.filePath(id), b, 0600); e != nil {
						t.Fatal(e)
					}
				}
				options := object(row.Get("options"))
				cursor := text(options.Get("scanCursor"))
				if cursor == "$NEXT" {
					cursor = last.NextScanCursor
				}
				token := text(options.Get("revisionToken"))
				if token == "$TOKEN" {
					token = last.RevisionToken
				}
				var v any
				var e error
				switch text(row.Get("method")) {
				case "overview":
					o, err := s.Overview(context.Background(), id, OverviewOptions{Offset: int64(number(options.Get("offset"))), Limit: int64(number(options.Get("limit"))), ScanCursor: cursor, RevisionToken: token})
					e = err
					v = o
					if e == nil {
						_ = json.Unmarshal(o.Get("window"), &last)
					}
				case "detail":
					o, err := s.Call(context.Background(), id, text(row.Get("callId")), "")
					e = err
					if o.IsObject() {
						v = o
					}
				case "full":
					v, e = s.Full(context.Background(), id)
				}
				out := obj("value", v)
				if e != nil {
					code := json.RawMessage("null")
					var own *Error
					if errors.As(e, &own) {
						code = jstr(own.Code)
					}
					out = obj("error", e.Error(), "code", code)
				}
				t.Run(integerString(int64(n)), func(t *testing.T) { compare(t, reduceObservations(expected[n]), reduceObservations(out)) })
			}
		})
	}
}
func fmtIndex(n int) string {
	s := integerString(int64(n))
	return strings.Repeat("0", max(0, 5-len(s))) + s
}
func TestCanonicalFallbackAfterFutureSchemaAndSameSizeRewrite(t *testing.T) {
	ctx := context.Background()
	h := fixtureHome(t)
	mode := "on"
	s, e := New(Options{Scope: filepath.Join(h, ".claude"), ResolveMode: func() string { return mode }})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(ctx)
	p := s.filePath("s")
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	first := boundedRecord("original")
	b := append(raw(obj("type", "call", "record", first)), '\n')
	if e = os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
	o, e := s.Overview(ctx, "s", OverviewOptions{})
	if e != nil {
		t.Fatal(e)
	}
	var w Window
	_ = json.Unmarshal(o.Get("window"), &w)
	if number(object(o.Get("summary")).Get("apiCalls")) != 1 {
		t.Fatal("Real projection absent")
	}
	stat, e := os.Stat(p)
	if e != nil {
		t.Fatal(e)
	}
	second := boundedRecord("rewritte")
	b = append(raw(obj("type", "call", "record", second)), '\n')
	if e = os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
	_ = os.Chtimes(p, stat.ModTime(), stat.ModTime())
	_, e = s.Overview(ctx, "s", OverviewOptions{RevisionToken: w.RevisionToken})
	var own *Error
	if !errors.As(e, &own) || own.Code != "TRACE_PAGE_STALE" {
		t.Fatalf("Same size rewrite accepted stale page: %v", e)
	}
	detail, e := s.Call(ctx, "s", "rewritte", "")
	if e != nil || !detail.IsObject() {
		t.Fatalf("Canonical detail lost: %v", e)
	}
	mode = "off"
	s.mode()
	dbpath := filepath.Join(s.o.Scope, "cc-haha", "db", "trace-index-v1.sqlite")
	db, e := localindex.OpenDatabase(ctx, localindex.DatabaseOptions{Path: dbpath, Scope: s.o.Scope, Kind: localindex.Trace})
	if e != nil {
		t.Fatal(e)
	}
	e = db.Write(ctx, func(op *localindex.Operation) error { return op.Exec("PRAGMA user_version=999") })
	_ = db.Close()
	if e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(dbpath)
	if e != nil {
		t.Fatal(e)
	}
	mode = "on"
	detail, e = s.Call(ctx, "s", "rewritte", "")
	if e != nil || !detail.IsObject() {
		t.Fatalf("Future DB blocked canonical detail: %v", e)
	}
	after, e := os.ReadFile(dbpath)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("Future database modified by fallback")
	}
	list, e := s.List(ctx, ListOptions{})
	if e != nil || len(array(list.Get("traces"))) != 1 {
		t.Fatalf("Actual file list unavailable without index: %v", e)
	}
}
func TestCloseJoinsQueuedCanonicalWriteEvenWithCancelledCaller(t *testing.T) {
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s, e := New(Options{Scope: fixtureHome(t), EntryPoint: "claude-desktop", BeforeAppend: func(context.Context) error { once.Do(func() { close(entered); <-release }); return nil }})
	if e != nil {
		t.Fatal(e)
	}
	capture := s.Bind(Binding{SessionID: "s"})(FetchInput{Context: ctx, Method: "POST", URL: "https://fixture.invalid/messages", PlainBody: []byte(`{"model":"fixture-model"}`)})
	if capture == nil {
		t.Fatal("Capture disabled")
	}
	<-entered
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	done := make(chan error, 1)
	go func() { done <- s.Close(cancelled) }()
	select {
	case <-done:
		t.Fatal("Close returned before owned canonical writer joined")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close failed to terminate uncaptured fetch outcome")
	}
	b, e := os.ReadFile(s.filePath("s"))
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(b, []byte(`"status":"error"`)) || !bytes.Contains(b, []byte(`"phase":"api_call_failed"`)) {
		t.Fatalf("Cancelled shutdown left pending actual fetch: %s", b)
	}
	if s.Bind(Binding{SessionID: "s"})(FetchInput{Context: ctx, Method: "POST", PlainBody: []byte(`{}`)}) != nil {
		t.Fatal("Closed service accepted capture")
	}
}
