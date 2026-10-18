package mcpruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualMCPPromptCacheSchemaLRUReconnectAndExecutionPolicy(t *testing.T) {
	var initializes, lists, gets atomic.Int32
	var mode atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var message protocol.JSONObject
		_ = json.NewDecoder(r.Body).Decode(&message)
		if message.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		var result json.RawMessage
		switch message.String("method") {
		case "initialize":
			initializes.Add(1)
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"prompts":{}},"serverInfo":{"name":"actual-prompt-cache","version":"1"}}`)
		case "prompts/list":
			lists.Add(1)
			result = []byte(`{"prompts":[{"name":"read.file","description":"original\u202e description","arguments":[{"name":"file"}]}],"nextCursor":"do-not-follow"}`)
			if mode.Load() == 1 {
				result = []byte(`{"prompts":[{"name":"read","arguments":[{"required":true}]}]}`)
			}
		case "prompts/get":
			gets.Add(1)
			result = []byte(`{"messages":[{"role":"user","content":{"type":"text","text":"physical prompt result"}}]}`)
		default:
			t.Error("unexpected actual prompt cache request", message)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: []byte(`"2.0"`)}, protocol.JSONField{Name: "id", Value: message.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	defer peer.Close()
	var enabled atomic.Bool
	enabled.Store(true)
	s := manager(t, func(context.Context, string, protocol.JSONObject) (bool, error) { return enabled.Load(), nil })
	config := parse(t, fmt.Sprintf(`{"type":"http","url":%q}`, peer.URL))
	first, err := s.Connect(context.Background(), "first", config)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := s.FetchPrompts(context.Background(), first)
			if err != nil || len(rows) != 1 || rows[0].String("description") != "original description" {
				t.Error("real shared prompt schema/sanitization/cache differs", rows, err)
			} else {
				rows[0].Set("name", []byte(`"caller-mutated"`))
			}
		}()
	}
	wg.Wait()
	rows, err := s.FetchPrompts(context.Background(), first)
	if err != nil || len(rows) != 1 || rows[0].String("name") != "read.file" || lists.Load() != 1 {
		t.Fatal("prompt cache leaked caller mutation/duplicated producer", rows, err, lists.Load())
	}
	for index := 1; index <= 20; index++ {
		connection, err := s.Connect(context.Background(), fmt.Sprintf("peer-%d", index), config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.FetchPrompts(context.Background(), connection); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.FetchPrompts(context.Background(), first); err != nil || lists.Load() != 22 || initializes.Load() != 21 {
		t.Fatal("20-entry prompt LRU evicted transport instead of cached list", err, lists.Load(), initializes.Load())
	}
	mode.Store(1)
	if err := s.InvalidatePrompts(first); err != nil {
		t.Fatal(err)
	}
	rows, err = s.FetchPrompts(context.Background(), first)
	if err != nil || len(rows) != 0 {
		t.Fatal("schema-invalid original prompt inventory was admitted", rows, err)
	}
	mode.Store(0)
	rows, err = s.FetchPrompts(context.Background(), first)
	if err != nil || len(rows) != 0 || lists.Load() != 23 {
		t.Fatal("caught prompt schema error was not cached as original empty result", rows, err, lists.Load())
	}
	if err := first.Client.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := s.GetPrompt(context.Background(), first, "read.file", protocol.NewJSONObject())
	if err != nil || result.Get("messages") == nil || gets.Load() != 1 || initializes.Load() != 22 {
		t.Fatal("prompt invocation did not reconnect through actual owning runtime", result, err, gets.Load(), initializes.Load())
	}
	foreign := manager(t, nil)
	if _, err := foreign.GetPrompt(context.Background(), first, "read.file", protocol.NewJSONObject()); err == nil {
		t.Fatal("foreign runtime invoked an actual peer")
	}
	enabled.Store(false)
	if _, err := s.GetPrompt(context.Background(), first, "read.file", protocol.NewJSONObject()); err == nil || gets.Load() != 1 {
		t.Fatal("prompt invocation bypassed execution policy", err, gets.Load())
	}
}

func TestActualMCPBlockedPromptDiscoveryCancelledWaitThenPhysicalCloseJoins(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var message protocol.JSONObject
		_ = json.NewDecoder(r.Body).Decode(&message)
		if message.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		if message.String("method") == "prompts/list" {
			close(started)
			<-r.Context().Done()
			close(cancelled)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: []byte(`"2.0"`)}, protocol.JSONField{Name: "id", Value: message.Get("id")}, protocol.JSONField{Name: "result", Value: []byte(`{"protocolVersion":"2025-06-18","capabilities":{"prompts":{}},"serverInfo":{"name":"actual-blocked-prompt","version":"1"}}`)}))
	}))
	defer peer.Close()
	s := manager(t, nil)
	connection, err := s.Connect(context.Background(), "blocked", parse(t, fmt.Sprintf(`{"type":"http","url":%q}`, peer.URL)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := s.FetchPrompts(ctx, connection); done <- err }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("physical prompt list not reached")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled cached prompt waiter succeeded", err)
	}
	closeCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := s.Close(closeCtx); err != nil {
		t.Fatal("prompt discovery held actual close", err)
	}
	select {
	case <-cancelled:
	case <-closeCtx.Done():
		t.Fatal("physical prompt list was not cancelled/joined on close")
	}
	if _, err := s.FetchPrompts(context.Background(), connection); !errors.Is(err, context.Canceled) {
		t.Fatal("closed runtime accepted cached prompt work", err)
	}
}
