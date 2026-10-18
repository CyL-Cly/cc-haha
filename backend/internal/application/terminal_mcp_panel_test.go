package application

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTerminalMCPPanelAndSlashTogglePersistAndReplaceNextExecutionCatalog(t *testing.T) {
	var initializations, requests atomic.Int32
	initializing := make(chan struct{}, 1)
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
		var result any
		switch message.String("method") {
		case "initialize":
			initializations.Add(1)
			select {
			case initializing <- struct{}{}:
			default:
			}
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "panel-peer", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "inspect", "description": "Panel inspector", "inputSchema": map[string]any{"type": "object"}}}}
		default:
			t.Error(message.String("method"))
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw(map[string]any{"jsonrpc": "2.0", "id": message.Get("id"), "result": result}))
	}))
	defer peer.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		n := requests.Add(1)
		present := strings.Contains(string(obj(data).Get("tools")), "mcp__panel__inspect")
		if present != (n == 2) {
			t.Error("MCP panel state not consumed by next request", n, present)
		}
		response(w, "mcp-panel-next", "", nil, "MCP_PANEL_NEXT")
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Interactive = true
	o.Arguments.Tools = []string{""}
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("private fixture Keychain unavailable")
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"mcpServers": map[string]any{"panel": map[string]any{"type": "http", "url": peer.URL}}, "future": "preserved"}), 0600)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	// Source starts the configuration read asynchronously and publishes the
	// pending AppState row before this real initialize request. Opening after
	// observing that primitive exercises a configured menu, without waiting
	// for tools/list readiness or weakening disable/next-request assertions.
	select {
	case <-initializing:
	case <-time.After(5 * time.Second):
		t.Fatal("actual configured MCP connection never started")
	}
	var panel *tui.LocalPanel
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		return nil
	}
	if _, err = s.InvokeTerminalCommand(context.Background(), "mcp", "", emit); err != nil || len(panel.Rows) != 1 {
		t.Fatal(err, panel)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Row: "panel", Action: "toggle"}, emit); err != nil || panel.Notice != "" || !strings.Contains(panel.Rows[0].Label, "disabled") {
		t.Fatal(err, panel)
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "Use disabled catalog"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InvokeTerminalCommand(context.Background(), "mcp", "enable panel", emit); err != nil {
		t.Fatal(err)
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "Use enabled catalog"}, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Row: "panel", Action: "reconnect"}, emit); err != nil || panel.Notice != "" {
		t.Fatal(err, panel)
	}
	if _, err = s.InvokeTerminalCommand(context.Background(), "mcp", "disable", emit); err != nil {
		t.Fatal(err)
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "Use disabled again"}, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(config, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]json.RawMessage
	_ = json.Unmarshal(data, &settings)
	if !strings.Contains(string(data), "disabledMcpServers") || string(settings["future"]) != `"preserved"` {
		t.Fatal(string(data))
	}
	if requests.Load() != 3 || initializations.Load() != 3 {
		t.Fatal(requests.Load(), initializations.Load())
	}
}
