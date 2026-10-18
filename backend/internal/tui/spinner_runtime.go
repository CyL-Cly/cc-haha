package tui

import (
	tea "charm.land/bubbletea/v2"
	"time"
)

type terminalSpinnerTicker struct {
	started  time.Time
	revision uint64
	running  bool
	blurred  bool
}

func (m *terminalModel) terminalSpinnerAnimationCommand() tea.Cmd {
	ticker := m.spinnerTicker
	if ticker == nil || !ticker.running {
		return nil
	}
	interval := 16 * time.Millisecond
	if ticker.blurred {
		interval *= 2
	}
	revision, started := ticker.revision, ticker.started
	return tea.Tick(interval, func(now time.Time) tea.Msg {
		return terminalSpinnerAnimationMsg{now: now, clockTime: now.Sub(started), revision: revision}
	})
}

func (m *terminalModel) syncTerminalSpinnerAnimation() tea.Cmd {
	if m.spinnerTicker == nil {
		m.spinnerTicker = &terminalSpinnerTicker{}
	}
	ticker := m.spinnerTicker
	active := !m.state.Preferences.ReducedMotion && m.state.terminalSpinnerMounted()
	if m.ctx != nil && m.ctx.Err() != nil {
		active = false
	}
	if active == ticker.running {
		return nil
	}
	ticker.running = active
	ticker.revision++
	if !active {
		return nil
	}
	if ticker.started.IsZero() {
		ticker.started = time.Now()
	}
	return m.terminalSpinnerAnimationCommand()
}

func (m *terminalModel) terminalSpinnerFocus(focused bool) tea.Cmd {
	if m.spinnerTicker == nil {
		m.spinnerTicker = &terminalSpinnerTicker{}
	}
	ticker := m.spinnerTicker
	if ticker.blurred == !focused {
		return nil
	}
	ticker.blurred = !focused
	ticker.revision++
	return m.terminalSpinnerAnimationCommand()
}
