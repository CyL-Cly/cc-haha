package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type TerminalCachedTeamsSource interface {
	SubscribeTerminalCachedTeams(context.Context, func([]protocol.JSONObject)) ([]protocol.JSONObject, func(), error)
}

type terminalCachedTeamsChangedMsg struct{ teams []protocol.JSONObject }

func cloneCachedTeams(teams []protocol.JSONObject) []protocol.JSONObject {
	values := make([]protocol.JSONObject, len(teams))
	for i, team := range teams {
		values[i] = team.Clone()
	}
	return values
}

func enqueueTerminalCachedTeams(ctx context.Context, queue chan []protocol.JSONObject, teams []protocol.JSONObject) {
	value := cloneCachedTeams(teams)
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

func (m *terminalModel) terminalCachedTeamsCommand() tea.Cmd {
	if m.cachedTeamsFrames != nil {
		return m.terminalCachedTeamsFrameCommand()
	}
	if m.cachedTeamsChanges == nil {
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
		case teams := <-m.cachedTeamsChanges:
			return terminalCachedTeamsChangedMsg{teams}
		}
	}
}

func (s *State) applyTerminalCachedTeams(teams []protocol.JSONObject) {
	s.CachedTeams = cloneCachedTeams(teams)
	s.selectedTerminalFooter()
}
