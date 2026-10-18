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

func TestTerminalMemoryPanelEditsRealProjectAndRefreshesNextSystemContext(t *testing.T) {
	var count atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := count.Add(1)
		// Native CLAUDE.md context is the current request's leading user
		// system-reminder, rather than an Anthropic system prompt block.
		system := string(obj(body).Get("messages"))
		expected := "OLD_PROJECT_MEMORY_217"
		if n == 2 {
			expected = "NEW_PROJECT_MEMORY_217"
			if strings.Contains(system, "OLD_PROJECT_MEMORY_217") {
				t.Error("old native memory memo survived editor")
			}
		}
		if !strings.Contains(system, expected) {
			t.Error("same Harness memory missing", expected)
		}
		response(w, "memory-panel", "", nil, "MEMORY_PANEL_NEXT")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	path := filepath.Join(o.WorkDir, "CLAUDE.md")
	if err := os.WriteFile(path, []byte("OLD_PROJECT_MEMORY_217"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err = s.Submit(context.Background(), tui.Input{Text: "Prime original memory"}, nil); err != nil {
		t.Fatal(err)
	}
	var panel *tui.LocalPanel
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		return nil
	}
	if _, err = s.InvokeTerminalCommand(context.Background(), "memory", "", emit); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range panel.Rows {
		found = found || row.ID == path
	}
	if !found {
		t.Fatal(panel)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "edit-memory", Row: path}, emit); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "save", Fields: map[string]string{"content": "NEW_PROJECT_MEMORY_217\n中文 memory"}}, emit); err != nil || panel.Notice != "" {
		t.Fatal(err, panel.Notice)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "NEW_PROJECT_MEMORY_217") {
		t.Fatal(err, string(data))
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "Use edited memory"}, nil); err != nil {
		t.Fatal(err)
	}
	var notice string
	if _, err = s.InvokeTerminalCommand(context.Background(), "output-style", "", func(event tui.Event) error { notice = event.Text; return nil }); err != nil || !strings.Contains(notice, "deprecated") {
		t.Fatal(err, notice)
	}
	if count.Load() != 2 {
		t.Fatal("local memory or style command requested model", count.Load())
	}
}
