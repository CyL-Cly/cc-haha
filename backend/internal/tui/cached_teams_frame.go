package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// TerminalCachedTeamsFrame is an opaque captured PromptInput footer value.
// The concrete application issuer verifies it before native dialog creation.
type TerminalCachedTeamsFrame interface{ InitialTeams() []protocol.JSONObject }
type TerminalCachedTeamsFrameSource interface {
	SubscribeTerminalCachedTeamsFrames(context.Context, func(TerminalCachedTeamsFrame)) (TerminalCachedTeamsFrame, func(), error)
}

type terminalCachedTeamsFrameChangedMsg struct {
	frame TerminalCachedTeamsFrame
	teams []protocol.JSONObject
}

func enqueueTerminalCachedTeamsFrame(ctx context.Context, queue chan terminalCachedTeamsFrameChangedMsg, frame TerminalCachedTeamsFrame) {
	var teams []protocol.JSONObject
	if frame != nil {
		teams = cloneCachedTeams(frame.InitialTeams())
	}
	value := terminalCachedTeamsFrameChangedMsg{frame: frame, teams: teams}
	select {
	case <-ctx.Done():
		return
	default:
	}
	select {
	case queue <- value:
		return
	default:
	}
	select {
	case <-queue:
	default:
	}
	select {
	case queue <- value:
	case <-ctx.Done():
	default:
	}
}
func (m *terminalModel) terminalCachedTeamsFrameCommand() tea.Cmd {
	return func() tea.Msg {
		if !m.beginTerminalUpdate() {
			return nil
		}
		defer m.turns.Done()
		select {
		case <-m.ctx.Done():
			return nil
		case value := <-m.cachedTeamsFrames:
			return value
		}
	}
}
