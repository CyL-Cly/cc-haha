package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"time"
)

type TerminalUpdateSession interface {
	CheckTerminalUpdate(context.Context, Emit) error
}

func (m *terminalModel) terminalUpdateCommand() tea.Cmd {
	host, ok := m.session.(TerminalUpdateSession)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		if !m.beginTerminalUpdate() {
			return nil
		}
		defer m.turns.Done()
		err := host.CheckTerminalUpdate(m.ctx, func(event Event) error {
			ack := make(chan struct{})
			m.program.Send(actionEventMsg{event: event, ack: ack})
			select {
			case <-ack:
				return nil
			case <-m.ctx.Done():
				return m.ctx.Err()
			}
		})
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return actionReturnedMsg{err}
	}
}

// Updates belong to terminal shutdown, while worktree exit waits only for
// foreground input callbacks. Waiting for this HTTP worker before Quit would
// keep its terminal context alive and prevent the request from being cancelled.
func (m *terminalModel) beginTerminalUpdate() bool {
	m.lifetime.Lock()
	defer m.lifetime.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return false
	}
	m.turns.Add(1)
	return true
}

func (m *terminalModel) terminalUpdateTick(now time.Time) tea.Cmd {
	if !now.Before(m.terminalUpdateUntil) {
		m.terminalUpdateUntil = now.Add(30 * time.Minute)
		return m.terminalUpdateCommand()
	}
	return nil
}
func (s *State) terminalUpdateLines() []string {
	value := s.TerminalUpdate
	switch value.String("phase") {
	case "checking":
		return wrap("Checking for updates", s.Width)
	case "success":
		return wrap("✓ Update installed · Restart to update", s.Width)
	case "install_failed":
		return wrap("✗ Auto-update failed · Try /status", s.Width)
	}
	return nil
}
func (s *State) applyTerminalUpdate(raw []byte) {
	var value protocol.JSONObject
	if value.UnmarshalJSON(raw) == nil {
		s.TerminalUpdate = value.Clone()
	}
}
