package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestInteractiveMCPActualHTTPFastPeerPublishesBeforeSlowInitializeAndCloseJoins(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		data, _ := io.ReadAll(r.Body)
		var message protocol.JSONObject
		_ = message.UnmarshalJSON(data)
		if r.URL.Path == "/slow" && message.String("method") == "initialize" {
			close(started)
			<-r.Context().Done()
			close(cancelled)
			return
		}
		if message.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		var result any
		switch message.String("method") {
		case "initialize":
			result = map[string]any{"protocolVersion": mcpclient.LatestProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "owned-fast", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "actual_fast_tool", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
		default:
			t.Error("unexpected interactive MCP request", message)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(message.Get("id")), "result": result})
	}))
	defer peer.Close()
	home := t.TempDir()
	runtime, err := mcpruntime.New(mcpruntime.Options{CWD: home, Environment: map[string]string{}, Auth: func(context.Context, string, protocol.JSONObject) (mcpclient.AuthProvider, error) { return nil, nil }, Enabled: func(context.Context, string, protocol.JSONObject) (bool, error) { return true, nil }, Authenticate: func(context.Context, string, protocol.JSONObject, func(string) error) error {
		return errors.New("fixture auth unavailable")
	}, ClearOAuth: func(context.Context, string, protocol.JSONObject) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	owner := cooperative.New()
	defer owner.Close()
	var references ownerasync.NativeReferences
	binding, err := references.Bind(owner)
	if err != nil {
		t.Fatal(err)
	}
	empty := func(context.Context) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }
	configs, err := mcpconfig.New(mcpconfig.Options{CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadFile: os.ReadFile, Environment: func(string) (string, bool) { return "", false }, SourceEnabled: func(string) bool { return false }, Settings: empty, Policy: empty, Global: empty, Project: empty, Plugins: func(context.Context) (mcpconfig.Result, error) {
		return mcpconfig.Result{Servers: protocol.NewJSONObject()}, nil
	}, Enabled: func(context.Context, string, protocol.JSONObject) (bool, error) { return true, nil }, BypassConfirmed: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	servers := protocol.NewJSONObject()
	for _, name := range []string{"slow", "fast"} {
		servers.Set(name, raw(map[string]string{"type": "http", "url": peer.URL + "/" + name}))
	}
	service, err := New(Options{Owner: owner, NativeBinding: binding, Runtime: runtime, Configurations: configs, All: func(context.Context) (mcpconfig.Result, error) {
		return mcpconfig.Result{Servers: servers.Clone()}, nil
	}, PermissionContext: func(*cooperative.Scope) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }, ScopeForContext: func(value any) *cooperative.Scope { scope, _ := value.(*cooperative.Scope); return scope }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	ready := make(chan struct{}, 1)
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, err := service.BeginInteractiveDiscovery(scope, func(next *cooperative.Scope) error {
			for _, connection := range service.connections {
				if connection.Name == "fast" && connection.Status == "connected" && len(connection.Tools) == 1 {
					select {
					case ready <- struct{}{}:
					default:
					}
				}
			}
			return nil
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("slow initialize did not actually start", ctx.Err())
	}
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("slow peer held fast tools behind startup discovery", ctx.Err())
	}
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		statuses, err := service.Status(scope)
		if err != nil {
			return err
		}
		if len(statuses) != 2 || statuses[0].String("status") != "pending" || statuses[1].String("status") != "connected" {
			t.Errorf("pending/connected Source snapshots lost: %v", statuses)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("runtime close did not cancel physical slow MCP", ctx.Err())
	}
	deadline := time.Now().Add(time.Second)
	for {
		count, _ := references.Snapshot()
		if count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("MCP async producer leaked actual native reference", count)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestInteractiveMCPDisableRetiresPendingPhysicalDiscoveryBeforeItsCompletion(t *testing.T) {
	listStarted, listCancelled := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		data, _ := io.ReadAll(r.Body)
		var message protocol.JSONObject
		_ = message.UnmarshalJSON(data)
		if message.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		if message.String("method") == "tools/list" {
			close(listStarted)
			<-r.Context().Done()
			close(listCancelled)
			return
		}
		if message.String("method") != "initialize" {
			t.Error("unexpected physical startup discovery request", message)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(message.Get("id")), "result": map[string]any{"protocolVersion": mcpclient.LatestProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "owned-retired", "version": "1"}}})
	}))
	defer peer.Close()
	home := t.TempDir()
	runtime := sdkRuntimeForPhysicalPeer(t, home, nil)
	owner := cooperative.New()
	defer owner.Close()
	var references ownerasync.NativeReferences
	binding, err := references.Bind(owner)
	if err != nil {
		t.Fatal(err)
	}
	empty := func(context.Context) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }
	configs, err := mcpconfig.New(mcpconfig.Options{CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadFile: os.ReadFile, Environment: func(string) (string, bool) { return "", false }, SourceEnabled: func(string) bool { return false }, Settings: empty, Policy: empty, Global: empty, Project: empty, Plugins: func(context.Context) (mcpconfig.Result, error) {
		return mcpconfig.Result{Servers: protocol.NewJSONObject()}, nil
	}, Enabled: func(context.Context, string, protocol.JSONObject) (bool, error) { return true, nil }, BypassConfirmed: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	servers := protocol.NewJSONObject()
	servers.Set("owned", raw(map[string]string{"type": "http", "url": peer.URL}))
	service, err := New(Options{Owner: owner, NativeBinding: binding, Runtime: runtime, Configurations: configs, All: func(context.Context) (mcpconfig.Result, error) {
		return mcpconfig.Result{Servers: servers.Clone()}, nil
	}, PermissionContext: func(*cooperative.Scope) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }, ScopeForContext: func(value any) *cooperative.Scope { scope, _ := value.(*cooperative.Scope); return scope }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, err := service.BeginInteractiveDiscovery(scope, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-listStarted:
	case <-ctx.Done():
		t.Fatal("actual pending startup tools/list did not start")
	}
	// Disable publishes immediately, then awaits physical disconnect on this
	// Owner. That await admits the old startup completion; it must never restore
	// either connected or failed status over the explicit disabled state.
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error { return service.DisableServer(scope, "owned") }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-listCancelled:
	case <-ctx.Done():
		t.Fatal("disable did not cancel physical pending tools/list")
	}
	for {
		count, idle := references.Snapshot()
		if count == 0 {
			break
		}
		select {
		case <-idle:
		case <-ctx.Done():
			t.Fatal("retired startup work did not settle", count)
		}
	}
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		statuses, err := service.Status(scope)
		if err != nil {
			return err
		}
		if len(statuses) != 1 || statuses[0].String("status") != "disabled" {
			t.Errorf("late original discovery overwrote immediate disabled state: %v", statuses)
		}
		definitions, registrations, err := service.Catalog(scope)
		if len(definitions) != 0 || len(registrations) != 0 {
			t.Error("retired startup peer remains in next execution catalog", definitions)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
