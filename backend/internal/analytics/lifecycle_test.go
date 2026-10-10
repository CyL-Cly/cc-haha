package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualConcurrentExportWaitsForItsOwnHTTPAndPreservesIndependentCompletion(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	first := make(chan struct{})
	second := make(chan struct{})
	release := make(chan struct{})
	var count atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if count.Add(1) == 1 {
			close(first)
			<-release
		} else {
			close(second)
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
	service := testService(t, owner, sink.URL, nil)
	var a, b *ownerasync.OwnerFuture[struct{}]
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var e error
		a, e = service.Export(scope, []protocol.JSONObject{protocol.NewJSONObject(field("private", 1))})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("first real request missing")
	}
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var e error
		b, e = service.Export(scope, []protocol.JSONObject{protocol.NewJSONObject(field("private", 2))})
		if e != nil {
			return e
		}
		_, e = b.Await(scope)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	select {
	case <-second:
	default:
		t.Fatal("second export reported completion before real HTTP")
	}
	close(release)
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, e := a.Await(scope)
		if e != nil {
			return e
		}
		return service.Flush(scope)
	}); e != nil {
		t.Fatal(e)
	}
}
func TestActualOldJSONLBoundedTailUnknownRowsAndPrivateModes(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "telemetry", "old.json")
	files := OSFiles{}
	if e := files.Append(path, []byte("{\"future\":17}\nnull\n[1,2]\n")); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("old event mode", info, e)
	}
	raw, e := files.ReadFile(path)
	if e != nil || len(decodeJSONLines(raw)) != 3 {
		t.Fatal("unknown legal old rows dropped", e)
	}
	file, e := os.OpenFile(path, os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	const max = 100 * 1024 * 1024
	if e = file.Truncate(max + 100); e != nil {
		t.Fatal(e)
	}
	tail := []byte("discarded partial\n{\"future\":19}\n")
	if _, e = file.WriteAt(tail, max+100-int64(len(tail))); e != nil {
		t.Fatal(e)
	}
	_ = file.Close()
	raw, e = files.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(raw, []byte("{\"future\":19}\n")) {
		t.Fatal("bounded tail not first-newline aligned", len(raw))
	}
	var object protocol.JSONObject
	_ = json.Unmarshal(decodeJSONLines(raw)[0], &object)
	if string(object.Get("future")) != "19" {
		t.Fatal("future payload changed")
	}
}
func TestActualPrivacySamplingEnrichmentFaultAndQueueNeverEmitsToPublicNetwork(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	var count atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); w.WriteHeader(204) }))
	defer sink.Close()
	for _, env := range []map[string]string{{"NODE_ENV": "test"}, {"DISABLE_TELEMETRY": "0"}, {"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "false"}, {"CLAUDE_CODE_USE_BEDROCK": "YES"}} {
		service := testService(t, owner, sink.URL, func(o *Options) { o.Environment = func(*cooperative.Scope) map[string]string { return env } })
		if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			if e := service.Log(scope, Event{Name: "private", Metadata: protocol.NewJSONObject()}); e != nil {
				return e
			}
			return service.Flush(scope)
		}); e != nil {
			t.Fatal(e)
		}
	}
	if count.Load() != 0 {
		t.Fatal("privacy controls emitted", count.Load())
	}
	service := testService(t, owner, sink.URL, func(o *Options) {
		o.Config = func(_ *cooperative.Scope, name string) protocol.JSONObject {
			if name == "tengu_event_sampling_config" {
				return protocol.NewJSONObject(field("drop", map[string]any{"sample_rate": 0}))
			}
			return protocol.NewJSONObject(field("baseUrl", sink.URL))
		}
	})
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if e := service.Log(scope, Event{Name: "drop", Metadata: protocol.NewJSONObject()}); e != nil {
			return e
		}
		return service.Flush(scope)
	}); e != nil {
		t.Fatal(e)
	}
	if count.Load() != 0 {
		t.Fatal("sample rate zero emitted")
	}
}
