package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/charmbracelet/x/ansi"
)

func TestTerminalHelpV2ActualGeneralTabsFirstCommandAndCanonicalClose(t *testing.T) {
	ctx := context.Background()
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Interactive = true
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	ui := tui.NewState(nil, 100, 80)
	opened := make(chan tui.Event, 1)
	emit := func(event tui.Event) error {
		if event.Kind == tui.LocalPanelOpened {
			opened <- event
		} else {
			ui.Apply(event)
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { _, err := s.InvokeTerminalCommand(ctx, "help", "", emit); done <- err }()
	select {
	case event := <-opened:
		ui.Apply(event)
	case <-time.After(3 * time.Second):
		t.Fatal("actual Help did not mount")
	}
	view := ansi.Strip(ui.View("", false).Content)
	if !strings.Contains(view, "Claude understands your codebase") || !strings.Contains(view, "custom-commands") {
		t.Fatal("Source HelpV2 general tab absent", view)
	}
	var data tui.TerminalHelpData
	if ui.LocalPanel == nil {
		t.Fatal("actual help panel absent")
	}
	// The app projection remains detached from the display's tab/selection state.
	s.mu.Lock()
	panel := s.terminalPanel
	s.mu.Unlock()
	if json.Unmarshal(panel.props.Presentation, &data) != nil {
		t.Fatal(string(panel.props.Presentation))
	}
	count := 0
	for _, row := range data.Commands {
		if row.String("name") == "clear" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("Help did not preserve the Source first command", count, data.Commands)
	}
	ui.HandleKey(tui.Key{Name: "tab"}, time.Now())
	view = ansi.Strip(ui.View("", false).Content)
	if !strings.Contains(view, "Browse default commands:") {
		t.Fatal("Help tabs did not select actual command browser", view)
	}
	if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.props.ID, Action: "help-close"}, emit); err != nil {
		t.Fatal(err)
	}
	if ui.LocalPanel != nil {
		t.Fatal("Help close did not unmount")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("actual Help onDone did not complete")
	}
	history, err := s.process.TerminalConversation(ctx)
	if err != nil || !strings.Contains(string(raw(history.Messages)), "Help dialog dismissed") {
		t.Fatal("Actual canonical system completion absent", err, string(raw(history.Messages)))
	}
}
