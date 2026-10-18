package tui

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

type statusLineMountTestSession struct {
	*testSession
	calls []struct {
		mounted bool
		vim     string
	}
}

func TestTerminalStatusLineActualCompletionAndBracketedPasteCommittedLifetimes(t *testing.T) {
	session := &statusLineMountTestSession{testSession: &testSession{}}
	m := &terminalModel{ctx: context.Background(), session: session, state: NewState([]Completion{{Name: "help", Kind: "local-jsx"}}, 100, 35)}
	m.syncTerminalStatusLineMount()
	m.state.Editor.Set("/he")
	m.state.updateCompletions()
	m.syncTerminalStatusLineMount()
	if len(m.state.Completions) == 0 || session.calls[len(session.calls)-1].mounted {
		t.Fatal("Actual scrollback completion did not unmount", session.calls)
	}
	m.options.Fullscreen = true
	m.syncTerminalStatusLineMount()
	if !session.calls[len(session.calls)-1].mounted {
		t.Fatal("Fullscreen Source completion portal should retain statusline", session.calls)
	}
	m.state.Editor.Reset()
	m.state.updateCompletions()
	m.options.Fullscreen = false
	m.syncTerminalStatusLineMount()
	m.Update(tea.PasteStartMsg{})
	if !m.state.Pasting || session.calls[len(session.calls)-1].mounted {
		t.Fatal("Physical bracketed paste-start kept native shell mounted", session.calls)
	}
	m.Update(tea.PasteEndMsg{})
	if m.state.Pasting || !session.calls[len(session.calls)-1].mounted {
		t.Fatal("Physical paste-end did not remount", session.calls)
	}
}

func (s *statusLineMountTestSession) SubscribeTerminalStatusLineMount(context.Context, bool, string, func(json.RawMessage)) (json.RawMessage, func(), error) {
	return nil, func() {}, nil
}
func (s *statusLineMountTestSession) SetTerminalStatusLineMounted(_ context.Context, mounted bool, vim string) error {
	s.calls = append(s.calls, struct {
		mounted bool
		vim     string
	}{mounted, vim})
	return nil
}

func TestTerminalStatusLineCommittedInputMountPreservesImmediateSidebarAndNoTickPolling(t *testing.T) {
	session := &statusLineMountTestSession{testSession: &testSession{}}
	m := &terminalModel{ctx: context.Background(), session: session, state: NewState(nil, 100, 35)}
	m.syncTerminalStatusLineMount()
	if len(session.calls) != 1 || !session.calls[0].mounted {
		t.Fatal("Initial real prompt mount", session.calls)
	}
	for i := 0; i < 3; i++ {
		m.state.Tick(time.Now())
		m.syncTerminalStatusLineMount()
	}
	if len(session.calls) != 1 {
		t.Fatal("Unchanged UI tick re-polled native statusline", session.calls)
	}
	m.state.LocalPanel = &localPanelState{props: LocalPanel{ID: "help", Kind: "help"}}
	m.syncTerminalStatusLineMount()
	if len(session.calls) != 2 || session.calls[1].mounted {
		t.Fatal("Actual modal transition did not synchronously unmount", session.calls)
	}
	m.state.LocalPanel = &localPanelState{props: LocalPanel{ID: "btw", Kind: "btw", KeepPromptInput: true}}
	m.syncTerminalStatusLineMount()
	if len(session.calls) != 3 || !session.calls[2].mounted {
		t.Fatal("Source busy BTW keeps actual prompt mounted", session.calls)
	}
	m.state.Mode = "bash"
	m.syncTerminalStatusLineMount()
	if session.calls[len(session.calls)-1].mounted {
		t.Fatal("Bash footer retained statusline")
	}
	m.state.Mode = "prompt"
	m.state.LocalPanel = nil
	m.options.Fullscreen = true
	m.state.Height = 23
	m.syncTerminalStatusLineMount()
	if len(session.calls) != 4 {
		t.Fatal("Short fullscreen recreated already unmounted worker", session.calls)
	}
	m.state.Height = 24
	m.syncTerminalStatusLineMount()
	if !session.calls[len(session.calls)-1].mounted {
		t.Fatal("Source 24-row threshold did not remount")
	}
	m.state.Preferences.EditorMode = "vim"
	m.syncTerminalStatusLineMount()
	if session.calls[len(session.calls)-1].vim != "INSERT" {
		t.Fatal("Actual Vim mode projection missing", session.calls)
	}
	m.state.Closing = true
	m.syncTerminalStatusLineMount()
	if session.calls[len(session.calls)-1].mounted {
		t.Fatal("Committed exit retained actual worker")
	}
}
