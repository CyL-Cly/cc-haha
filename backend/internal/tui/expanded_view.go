package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/NanmiCoder/cc-haha/backend/internal/teamnavigation"
)

type TerminalExpandedViewSource interface {
	TerminalExpandedView(context.Context) (string, error)
	SetTerminalExpandedView(context.Context, string) error
}

// The receipt exposes completion only, with no Scope or state-write authority.
type TerminalExpandedViewCommit interface{ Wait() error }

type TerminalExpandedViewQueueSource interface {
	EnqueueTerminalExpandedView(context.Context, string) (TerminalExpandedViewCommit, error)
}

type terminalExpandedViewReturnedMsg struct{ err error }

func (s *State) setTerminalExpandedView(value string) {
	if s.Teammates == nil {
		state := teamnavigation.New()
		s.Teammates = &state
	}
	s.Teammates.ExpandedView = value
}

func (s *State) terminalExpandedView() string {
	if s.Teammates == nil {
		return "none"
	}
	return s.Teammates.ExpandedView
}

func (m *terminalModel) syncTerminalExpandedView() tea.Cmd {
	value := m.state.terminalExpandedView()
	if m.expandedView == value {
		return nil
	}
	m.expandedView = value
	if source, ok := m.session.(TerminalExpandedViewQueueSource); ok {
		// Admit each transition before processing the next Tea event. Waiting
		// here would prevent the same Owner's current output from reaching Tea.
		receipt, err := source.EnqueueTerminalExpandedView(m.ctx, value)
		if err != nil {
			if m.ctx.Err() == nil {
				m.state.Status = err.Error()
			}
			return nil
		}
		return func() tea.Msg {
			if !m.beginTerminalUpdate() {
				return nil
			}
			defer m.turns.Done()
			return terminalExpandedViewReturnedMsg{receipt.Wait()}
		}
	}
	if source, ok := m.session.(TerminalExpandedViewSource); ok {
		// Legacy finite consumers retain their synchronous implementation.
		if err := source.SetTerminalExpandedView(m.ctx, value); err != nil && m.ctx.Err() == nil {
			m.state.Status = err.Error()
		}
	}
	return nil
}
