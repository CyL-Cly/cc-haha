package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestActualPublicDoctorMCPDiscoverySchemasAndAuxiliaryCountConsumer(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	config := filepath.Join(home, ".claude")
	managed := filepath.Join(home, "managed")
	for _, directory := range []string{config, managed} {
		if e := os.MkdirAll(directory, 0700); e != nil {
			t.Fatal(e)
		}
	}
	var rpc, counters, closed atomic.Int32
	streamOpened, streamClosed := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.Header.Get("Mcp-Session-Id") != "physical-doctor-session" {
				t.Error("Doctor opened the wrong MCP session")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			close(streamOpened)
			<-r.Context().Done()
			closed.Add(1)
			close(streamClosed)
			return
		}
		if r.Method == http.MethodDelete {
			t.Error("Doctor Close must not terminate the server session")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Method != http.MethodPost {
			t.Error("unexpected diagnostic transport method", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			ID     json.RawMessage
			Method string
		}
		if e := json.NewDecoder(r.Body).Decode(&request); e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		if strings.HasPrefix(request.Method, "notifications/") {
			w.WriteHeader(202)
			return
		}
		rpc.Add(1)
		var result any
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "physical-doctor-session")
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "physical-doctor-peer", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "actual_probe", "description": "DOCTOR_MCP_DESCRIPTION", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"marker": map[string]any{"type": "string"}}, "required": []string{"marker"}}}}}
		default:
			t.Error("unexpected diagnostic RPC", request.Method)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer peer.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counters.Add(1)
		select {
		case <-streamOpened:
		case <-r.Context().Done():
			t.Error("actual MCP SSE stream never opened")
			return
		}
		if r.URL.Path != "/v1/messages/count_tokens" {
			t.Error("Doctor started generation instead of auxiliary count", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		for _, fragment := range []string{"mcp__fixture__actual_probe", "DOCTOR_MCP_DESCRIPTION", `"input_schema"`, `"required":["marker"]`} {
			if !bytes.Contains(raw, []byte(fragment)) {
				t.Error("actual MCP definition missing from auxiliary request", fragment, string(raw))
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":30500}`))
	}))
	defer provider.Close()
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) }))
	defer releases.Close()
	global := map[string]any{"future": map[string]any{"keep": true}, "mcpServers": map[string]any{"fixture": map[string]any{"type": "http", "url": peer.URL}}}
	raw, _ := json.Marshal(global)
	agentsWrite(t, filepath.Join(config, ".claude.json"), string(raw))
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "PATH": "/usr/bin:/bin", "SHELL": "/bin/bash", "USER_TYPE": "external", "NODE_ENV": "production", "CC_HAHA_CLI_RELEASE_API": releases.URL, "ANTHROPIC_API_KEY": "fixture-key", "ANTHROPIC_MODEL": "fixture-model", "ANTHROPIC_BASE_URL": provider.URL, "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1", "ENABLE_TOOL_SEARCH": "true"}
	n, e := NewProcessNativeBoundary(NativeProcessOptions{Environment: env, CWD: home, Platform: "darwin", ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if e != nil {
		t.Fatal(e)
	}
	n.Temp = home
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("private keychain absent")
	}
	bindCommandPrefetchFixture(t, n)
	m, e := NewMainCommand(MainCommandOptions{Arguments: []string{"doctor"}, Native: n, Platform: "darwin", Version: "fixture", PID: os.Getpid(), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var report string
	e = m.Run(ctx, func(scope *cooperative.Scope) error { var e error; report, e = m.Doctor(ctx, scope); return e })
	if e != nil {
		t.Fatal(e)
	}
	if rpc.Load() != 2 || counters.Load() != 1 {
		t.Fatal("actual discovery/count producer cardinality", rpc.Load(), counters.Load(), report)
	}
	select {
	case <-streamClosed:
	case <-ctx.Done():
		t.Fatal("Doctor did not cancel its real MCP SSE stream", ctx.Err())
	}
	if closed.Load() != 1 {
		t.Fatal("Doctor did not retire its actual MCP transport", closed.Load())
	}
	if !strings.Contains(report, "fixture: connected (1 tools)") || !strings.Contains(report, "Large MCP tools context (~30,000 tokens > 25,000)") || strings.Contains(report, "deferred") {
		t.Fatal("actual standalone Doctor omitted schemas or changed MCP-only Source deferral", report)
	}
	if paths, _ := filepath.Glob(filepath.Join(config, "projects", "*", "*.jsonl")); len(paths) != 0 {
		t.Fatal("Doctor created an ASK transcript", paths)
	}
}
