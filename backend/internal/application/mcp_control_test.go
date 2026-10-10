package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
)

func TestDesktopMCPReconnectReplacesNextTurnCatalogAndReloadDropsDisabledTools(t *testing.T) {
	var generation, initializations, calls, modelCalls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		data, _ := io.ReadAll(r.Body)
		message := obj(data)
		if message.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		name := "first"
		if generation.Load() != 0 {
			name = "second"
		}
		var value any
		switch message.String("method") {
		case "initialize":
			initializations.Add(1)
			value = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "controlled-peer", "version": name}}
		case "tools/list":
			value = map[string]any{"tools": []any{map[string]any{"name": name, "description": "Return current generation.", "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "openWorldHint": true}}}}
		case "tools/call":
			calls.Add(1)
			if obj(message.Get("params")).String("name") != name {
				t.Error("stale tool executed after reconnect")
			}
			value = map[string]any{"content": []any{map[string]any{"type": "text", "text": name}}}
		default:
			t.Errorf("unexpected peer method %s", message.String("method"))
			value = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw(map[string]any{"jsonrpc": "2.0", "id": message.Get("id"), "result": value}))
	}))
	defer peer.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		switch modelCalls.Add(1) {
		case 1:
			if !bytes.Contains(data, []byte(`"name":"mcp__owned__first"`)) {
				t.Error("initial catalog missing first tool")
			}
			response(w, "first", "mcp__owned__first", map[string]any{}, "")
		case 3:
			if !bytes.Contains(data, []byte(`"name":"mcp__owned__second"`)) || bytes.Contains(obj(data).Get("tools"), []byte("mcp__owned__first")) {
				t.Error("reconnect did not replace next-turn catalog")
			}
			response(w, "second", "mcp__owned__second", map[string]any{}, "")
		case 5:
			if bytes.Contains(obj(data).Get("tools"), []byte("mcp__owned__")) {
				t.Error("disabled tools survived reload in next inference")
			}
			response(w, "disabled", "", nil, "disabled")
		default:
			response(w, "finished", "", nil, "finished")
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Arguments.Tools = []string{""}
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	global := raw(map[string]any{"mcpServers": map[string]any{"owned": map[string]any{"type": "http", "url": peer.URL, "headers": map[string]string{"X-Private": "fake"}, "oauth": map[string]any{"clientId": "fixture"}}}})
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), global, 0600); err != nil {
		t.Fatal(err)
	}
	live, err := DesktopFactory(o)(context.Background(), server.SessionOptions{ID: "30d3396c-3289-4c74-b6d6-bdb2f5c728c0", WorkDir: o.WorkDir}, func(json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	d := live.(*DesktopSession)
	defer d.Close(context.Background())
	status, err := d.ControlComponents(context.Background(), "mcp_status", "")
	if err != nil || !bytes.Contains(status.Get("mcpServers"), []byte(`"annotations":{"readOnly":true,"openWorld":true}`)) || !bytes.Contains(status.Get("mcpServers"), []byte(`"scope":"user"`)) {
		t.Fatalf("full native status: %s %v", raw(status), err)
	}
	turn := func(text string) {
		t.Helper()
		if err := d.Handle(context.Background(), raw(map[string]any{"type": "user_message", "content": text})); err != nil {
			t.Fatal(err)
		}
	}
	turn("use first")
	if _, err = d.ControlComponents(context.Background(), "mcp_reconnect", "missing"); err == nil || !strings.Contains(err.Error(), "Server not found: missing") {
		t.Fatal("unknown server response changed", err)
	}
	generation.Store(1)
	if _, err = d.ControlComponents(context.Background(), "mcp_reconnect", "owned"); err != nil {
		t.Fatal(err)
	}
	turn("use second")
	if calls.Load() != 2 || initializations.Load() != 2 {
		t.Fatalf("reconnect skipped actual transport: calls=%d init=%d", calls.Load(), initializations.Load())
	}
	globalConfig := obj(global)
	canonical, err := filepath.EvalSymlinks(o.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	globalConfig.Set("projects", raw(map[string]any{canonical: map[string]any{"disabledMcpServers": []string{"owned"}}}))
	if err = os.WriteFile(filepath.Join(config, ".claude.json"), raw(globalConfig), 0600); err != nil {
		t.Fatal(err)
	}
	// Complete the actual external file/watcher boundary before the next turn.
	// The old raw fixture snapshot omitted the identity created after startup.
	modified := time.Now().Add(time.Second)
	if err = os.Chtimes(filepath.Join(config, ".claude.json"), modified, modified); err != nil {
		t.Fatal(err)
	}
	watchCtx, stopWatch := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopWatch()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		seen := false
		if err = d.engine.process.Owner.Run(watchCtx, func(*cooperative.Scope) error {
			current, err := d.engine.process.Global.Get()
			seen = err == nil && current.Get("userID") == nil
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if seen {
			break
		}
		select {
		case <-poll.C:
		case <-watchCtx.Done():
			t.Fatal("actual external global refresh absent")
		}
	}
	reloaded, err := d.ControlComponents(context.Background(), "reload_plugins", "")
	if err != nil || !bytes.Contains(reloaded.Get("mcpServers"), []byte(`"status":"disabled"`)) {
		t.Fatalf("reload ignored actual enablement: %s %v", raw(reloaded), err)
	}
	turn("describe remaining tools")
	if calls.Load() != 2 || modelCalls.Load() != 5 {
		t.Fatal("disabled peer unexpectedly executed", calls.Load(), modelCalls.Load())
	}
}
