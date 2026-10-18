package tui

import (
	"context"
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
	"time"
)

type shortcutHelpTestSession struct {
	*testSession
	value TerminalShortcutHelp
	calls int
}

func (s *shortcutHelpTestSession) TerminalShortcutHelp(context.Context) (TerminalShortcutHelp, error) {
	s.calls++
	return s.value, nil
}

func TestPromptShortcutHelpMountSamplesActualSourceAndRejectsRetiredResult(t *testing.T) {
	session := &shortcutHelpTestSession{testSession: &testSession{}, value: TerminalShortcutHelp{Newline: "shift + ⏎ for newline", KeybindingsEnabled: true}}
	m := &terminalModel{state: NewState(nil, 100, 35), session: session, ctx: context.Background()}
	m.state.HandleKey(Key{Text: "?"}, time.Now())
	cmd := m.terminalShortcutHelpCommand()
	if cmd == nil {
		t.Fatal("Actual new Help mount did not sample its source")
	}
	late := cmd()
	m.state.HandleKey(Key{Name: "esc"}, time.Now())
	m.terminalShortcutHelpCommand()
	m.Update(late)
	if m.state.HelpOpen || m.state.ShortcutHelp.KeybindingsEnabled {
		t.Fatal("Retired footer result reopened or changed next mount state")
	}
	m.state.HandleKey(Key{Text: "?"}, time.Now())
	m.Update(m.terminalShortcutHelpCommand()())
	if session.calls != 2 || m.state.ShortcutHelp != session.value {
		t.Fatal("Next real mount reused stale source inputs", session.calls, m.state.ShortcutHelp)
	}
	if m.terminalShortcutHelpCommand() != nil {
		t.Fatal("Ordinary same-mount render reloaded memoized Source instructions")
	}
	m.turns.Wait()
}

func TestPromptShortcutHelpActualQuestionInputAndDismissConsumer(t *testing.T) {
	s := NewState(nil, 100, 35)
	if actions := s.HandleKey(Key{Name: "?", Text: "?"}, time.Now()); len(actions) > 0 {
		t.Fatal("Shortcut help sent a command or model action", actions)
	}
	view := ansi.Strip(s.View("", false).Content)
	if s.Editor.Text() != "" || !strings.Contains(view, "! for bash mode") || !strings.Contains(view, "/btw for side question") {
		t.Fatal("Original onChange('?') must toggle the actual shortcut help without changing input", s.Editor.Text(), view)
	}
	s.HandleKey(Key{Name: "esc"}, time.Now())
	if s.MessageSelector != nil || strings.Contains(ansi.Strip(s.View("", false).Content), "! for bash mode") {
		t.Fatal("Source Help dismissal escaped into another consumer")
	}
	s.HandleKey(Key{Name: "?", Text: "?"}, time.Now())
	s.HandleKey(Key{Name: "?", Text: "?"}, time.Now())
	if s.Editor.Text() != "" || strings.Contains(ansi.Strip(s.View("", false).Content), "! for bash mode") {
		t.Fatal("Second exact-question onChange must toggle help closed")
	}
}
