package application

import (
	"context"
	"encoding/json"
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

func TestTerminalPermissionPanelChangesActualCatalogAndPersistsWithoutModelForMenus(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		calls.Add(1)
		if strings.Contains(string(data), `"name":"Write"`) {
			t.Error("saved deny rule missing from actual next request")
		}
		if strings.Contains(string(data), "/permissions") {
			t.Error("permissions panel entered model history")
		}
		if strings.Count(string(data), "Help dialog dismissed") != 1 || strings.Count(string(data), "<command-name>/help</command-name>") != 1 {
			t.Error("actual ordinary Help onDone canonical records missing from next request")
		}
		response(w, "panel-next", "", nil, "PANEL_NEXT_OK")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.Native.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var panel *tui.LocalPanel
	emit := func(event tui.Event) error {
		if event.Kind == tui.LocalPanelOpened {
			copy := *event.LocalPanel
			panel = &copy
		}
		return nil
	}
	for _, name := range []string{"help", "tasks", "skills", "agents", "mcp", "allowed-tools", "bashes", "permissions"} {
		var submitted chan error
		if name == "help" {
			opened := make(chan struct{}, 1)
			submitted = make(chan error, 1)
			go func() {
				submitted <- s.Submit(context.Background(), tui.Input{Text: "/help", Mode: "prompt"}, func(event tui.Event) error {
					if err := emit(event); err != nil {
						return err
					}
					if event.Kind == tui.LocalPanelOpened {
						opened <- struct{}{}
					}
					return nil
				})
			}()
			select {
			case <-opened:
			case <-time.After(3 * time.Second):
				t.Fatal("actual Help did not open")
			}
			select {
			case err = <-submitted:
				t.Fatal("Help completed before actual onDone", err)
			default:
			}
		} else if err = s.Submit(context.Background(), tui.Input{Text: "/" + name, Mode: "prompt"}, emit); err != nil || panel == nil {
			t.Fatal(name, err)
		}
		action := "close"
		if name == "help" {
			action = "help-close"
		}
		if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: action}, emit); err != nil {
			t.Fatal(err)
		}
		if submitted != nil {
			select {
			case err = <-submitted:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Help onDone did not complete original Submit")
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatal("menus requested model", calls.Load())
	}
	_, err = s.InvokeTerminalCommand(context.Background(), "permissions", "", emit)
	if err != nil {
		t.Fatal(err)
	}
	id := panel.ID
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "add"}, emit); err != nil {
		t.Fatal(err)
	}
	if len(panel.Fields) != 3 {
		t.Fatal(panel)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "save", Fields: map[string]string{"behavior": "deny", "source": "userSettings", "rule": "Write"}}, emit); err != nil {
		t.Fatal(err)
	}
	if panel.Notice != "" || len(panel.Rows) == 0 {
		t.Fatal(panel)
	}
	data, err := os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"))
	if err != nil || !strings.Contains(string(data), `"Write"`) {
		t.Fatal(err, string(data))
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "close"}, emit); err != nil {
		t.Fatal(err)
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "Continue after panel", Mode: "prompt"}, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	_, err = s.InvokeTerminalCommand(context.Background(), "permissions", "", emit)
	if err != nil {
		t.Fatal(err)
	}
	row := ""
	for _, candidate := range panel.Rows {
		if strings.Contains(candidate.ID, "userSettings") && strings.Contains(candidate.ID, "Write") {
			row = candidate.ID
		}
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "remove", Row: row}, emit); err != nil {
		t.Fatal(err, panel)
	}
	for _, candidate := range panel.Rows {
		if candidate.ID == row {
			t.Fatal("removed live rule still exists")
		}
	}
	data, _ = os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"))
	var settings map[string]any
	if json.Unmarshal(data, &settings) != nil {
		t.Fatal(string(data))
	}
}

func TestTerminalAgentPanelCreatesEditsAndDeletesActualSharedDefinition(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Interactive = true
	o.Native.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
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
	if _, err = s.InvokeTerminalCommand(context.Background(), "agents", "", emit); err != nil {
		t.Fatal(err)
	}
	id := panel.ID
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "create"}, emit); err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{"name": "panel-worker", "scope": "project", "description": "actual panel reviewer", "systemPrompt": "ACTUAL_PANEL_AGENT_PROMPT", "model": "inherit", "tools": "Read"}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "save", Fields: fields}, emit); err != nil || panel.Notice != "" {
		t.Fatal(err, panel.Notice)
	}
	var row string
	for _, value := range panel.Rows {
		if strings.Contains(value.Label, "panel-worker") {
			row = value.ID
		}
	}
	if row == "" {
		t.Fatal(panel)
	}
	definitions, err := s.process.AgentDefinitions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, definition := range definitions.Active {
		found = found || definition.Name == "panel-worker" && strings.Contains(definition.SystemPrompt, "ACTUAL_PANEL_AGENT_PROMPT")
	}
	if !found {
		t.Fatal(definitions)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "edit", Row: row}, emit); err != nil {
		t.Fatal(err)
	}
	fields["description"] = "actual edited reviewer"
	fields["systemPrompt"] = "ACTUAL_PANEL_EDITED_PROMPT"
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "save", Fields: fields}, emit); err != nil || panel.Notice != "" {
		t.Fatal(err, panel.Notice)
	}
	data, err := os.ReadFile(filepath.Join(o.WorkDir, ".claude", "agents", "panel-worker.md"))
	if err != nil || !strings.Contains(string(data), "ACTUAL_PANEL_EDITED_PROMPT") {
		t.Fatal(err, string(data))
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "delete", Row: row}, emit); err != nil || panel.Notice != "" {
		t.Fatal(err, panel.Notice)
	}
	if _, err = os.Stat(filepath.Join(o.WorkDir, ".claude", "agents", "panel-worker.md")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	definitions, err = s.process.AgentDefinitions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions.Active {
		if definition.Name == "panel-worker" {
			t.Fatal("deleted definition remains active")
		}
	}
}
