package application

import (
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

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalMCPParsingDiagnosticsFollowActualListMountAndNextHuman(t *testing.T) {
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		response(w, "diagnostics-next", "", nil, "MCP_DIAGNOSTICS_NEXT_HUMAN")
	}))
	defer model.Close()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		message := obj(body)
		if message.Get("id") == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result := map[string]any{}
		switch message.String("method") {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "diagnostics-peer", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": message.Get("id"), "result": result})
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Interactive = true
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"mcpServers": map[string]any{"owned": map[string]string{"type": "http", "url": peer.URL}}}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(o.WorkDir), ".mcp.json"), []byte(`{"mcpServers":{"warning-peer":{"type":"http","url":"http://127.0.0.1:9/${CC_HAHA_WARNING_MISSING}"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(o.WorkDir, ".mcp.json")
	if err := os.WriteFile(project, []byte(`{broken`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var panel tui.LocalPanel
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			panel = terminalPanelProjection(*event.LocalPanel)
		}
		return nil
	}
	if _, err = s.InvokeTerminalCommand(context.Background(), "mcp", "", emit); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"[Failed to parse] Project config (shared via .mcp.json)", "Location: " + project, "[Error] MCP config is not a valid JSON", "[Warning] [warning-peer] mcpServers.warning-peer: Missing environment variables: CC_HAHA_WARNING_MISSING"} {
		if !strings.Contains(panel.Description, text) {
			t.Fatalf("real /mcp mount omitted Source parser diagnostic %q: %s", text, panel.Description)
		}
	}
	if err = os.WriteFile(project, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "mcp-catalog-refresh"}, emit); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(panel.Description, "MCP config is not a valid JSON") {
		t.Fatal("live peer refresh remounted parser warning component", panel.Description)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "mcp-open", Row: "owned"}, emit); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "mcp-back"}, emit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(panel.Description, "MCP config is not a valid JSON") || !strings.Contains(panel.Description, "[Contains warnings]") {
		t.Fatal("new list mount did not consume current project parser state", panel.Description)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "close"}, emit); err != nil {
		t.Fatal(err)
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "Next human after actual parser diagnostics"}, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("diagnostic menu invoked a model", calls.Load())
	}
}
