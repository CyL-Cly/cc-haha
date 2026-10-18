package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

type TerminalFullscreenSource interface {
	SubscribeTerminalFullscreen(context.Context, func()) (bool, func(), error)
	ReadTerminalFullscreen(context.Context) (bool, error)
}

type terminalFullscreenChangedMsg struct {
	enabled bool
	err     error
}

func enqueueTerminalFullscreen(ctx context.Context, changes chan struct{}) {
	select {
	case <-ctx.Done():
		return
	default:
	}
	select {
	case changes <- struct{}{}:
	default:
	}
}

func (m *terminalModel) terminalFullscreenCommand() tea.Cmd {
	source, ok := m.session.(TerminalFullscreenSource)
	if !ok || m.fullscreenChanges == nil {
		return nil
	}
	return func() tea.Msg {
		if !m.beginTerminalUpdate() {
			return nil
		}
		defer m.turns.Done()
		select {
		case <-m.ctx.Done():
			return nil
		case <-m.fullscreenChanges:
		}
		enabled, err := source.ReadTerminalFullscreen(m.ctx)
		return terminalFullscreenChangedMsg{enabled, err}
	}
}

func (m *terminalModel) applyTerminalFullscreen(enabled bool) {
	if m.options.Fullscreen == enabled && m.state.fullscreen == enabled {
		return
	}
	m.options.Fullscreen, m.state.fullscreen = enabled, enabled
	// The original AlternateScreen wrapper changes component identity. Only
	// Help's independently proven mount state is projected here; canonical
	// history, owned completion waits and queued input are retained.
	m.state.remountTerminalHelp()
}
