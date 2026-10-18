package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTerminalHooksPanelReadsActualConfigurationWithoutExecutingItAndNextPromptStillRunsHook(t *testing.T) {
	var count atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		count.Add(1)
		if !strings.Contains(string(data), "HOOK_LOCAL_CONTEXT_563") {
			t.Error("same hook consumer lost next prompt output")
		}
		response(w, "hook-panel-next", "", nil, "HOOK_PANEL_NEXT")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	marker := filepath.Join(o.WorkDir, "hook-ran")
	path := filepath.Join(config, "settings.json")
	settings := raw(map[string]any{"hooks": map[string]any{"UserPromptSubmit": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "printf YES > " + marker + "; printf HOOK_LOCAL_CONTEXT_563"}}}}}, "future": "preserved"})
	if err := os.WriteFile(path, settings, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var panel *tui.LocalPanel
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		return nil
	}
	if _, err = s.InvokeTerminalCommand(context.Background(), "hooks", "", emit); err != nil || len(panel.Rows) != 1 {
		t.Fatal(err, panel)
	}
	if len(panel.Rows[0].Actions) != 0 || len(panel.Fields) != 0 || !strings.Contains(panel.Rows[0].Detail, "HOOK_LOCAL_CONTEXT_563") {
		t.Fatal("Source hooks browser must remain read-only", panel)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) || count.Load() != 0 {
		t.Fatal("opening hook browser executed hook or model", err, count.Load())
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "close"}, emit); err != nil {
		t.Fatal(err)
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "Continue after hooks"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(marker); err != nil || count.Load() != 1 {
		t.Fatal(err, count.Load())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(settings) {
		t.Fatal("read-only hook panel mutated settings", err)
	}
}
