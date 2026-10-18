package application

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalThemePreviewSyntaxChildCancelAndConfigRollbackSharedStores(t *testing.T) {
	ctx := context.Background()
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Interactive = true
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	userPath := filepath.Join(config, "settings.json")
	if err := os.WriteFile(userPath, []byte(`{"futureUser":{"preserved":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	if err = s.process.SetTerminalConfigSetting(ctx, "theme", raw("dark")); err != nil {
		t.Fatal(err)
	}
	var panel *tui.LocalPanel
	var preferences *tui.TerminalPreferences
	var events []tui.Event
	emit := func(event tui.Event) error {
		events = append(events, event)
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		if event.Preferences != nil {
			copy := *event.Preferences
			preferences = &copy
		}
		return nil
	}
	open := func(name string) {
		t.Helper()
		if handled, err := s.InvokeTerminalCommand(ctx, name, "", emit); !handled || err != nil {
			t.Fatal(handled, err)
		}
	}
	act := func(action, row string) {
		t.Helper()
		if err := s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: action, Row: row}, emit); err != nil || panel.Notice != "" {
			t.Fatal(err, panel.Notice)
		}
	}
	user := func() string {
		t.Helper()
		data, err := os.ReadFile(userPath)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	open("config")
	act("edit-config", "theme")
	if !panel.ThemePicker || !panel.Submenu || len(panel.Rows) != 6 || panel.Rows[0].ID != "dark" {
		t.Fatal(panel)
	}
	state := tui.NewState(nil, 100, 32)
	state.Editor.Set("preserved draft")
	state.SetTerminalPreferences(tui.TerminalPreferences{Theme: "dark"})
	state.Apply(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: panel})
	state.HandleKey(tui.Key{Name: "down"}, time.Now())
	if state.Editor.Text() != "preserved draft" {
		t.Fatal("preview consumed draft")
	}
	global, err := s.process.Global.Get()
	if err != nil || global.String("theme") != "dark" {
		t.Fatal(err, global)
	}
	if user() != `{"futureUser":{"preserved":true}}` {
		t.Fatal("focus persisted preview", user())
	}
	act("theme-toggle-syntax", "")
	if obj([]byte(user())).Get("syntaxHighlightingDisabled") == nil || preferences == nil {
		t.Fatal("actual user setting consumer missing", user())
	}
	act("theme-cancel", "")
	if panel.ThemePicker || panel.Submenu || panel.Kind != "config" || string(obj([]byte(user())).Get("syntaxHighlightingDisabled")) != "true" {
		t.Fatal("child cancel incorrectly restored outer snapshot", panel, user())
	}
	act("close", "")
	if obj([]byte(user())).Get("syntaxHighlightingDisabled") != nil || string(obj(obj([]byte(user())).Get("futureUser")).Get("preserved")) != "true" {
		t.Fatal("outer Escape failed absent-value restoration", user())
	}
	open("config")
	act("edit-config", "theme")
	act("theme-toggle-syntax", "")
	act("theme-select", "light-ansi")
	if preferences.Theme != "light-ansi" || panel.ThemePicker {
		t.Fatal(preferences, panel)
	}
	act("commit-config", "")
	global, _ = s.process.Global.Get()
	if global.String("theme") != "light-ansi" || string(obj([]byte(user())).Get("syntaxHighlightingDisabled")) != "true" {
		t.Fatal(global, user())
	}
	open("theme")
	act("theme-cancel", "")
	last := events[len(events)-1]
	if last.Kind != tui.LocalProcessedMessages || len(last.LocalMessages) != 1 || last.LocalMessages[0].Kind != "system" || last.LocalMessages[0].Text != "Theme picker dismissed" {
		t.Fatal("actual Source cancel display category", last)
	}
}
