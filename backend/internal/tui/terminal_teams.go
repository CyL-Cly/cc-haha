package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/google/uuid"
)

type TerminalTeamsRequest struct {
	ID           string
	InitialTeams []protocol.JSONObject
	InitialFrame TerminalCachedTeamsFrame `json:"-"`
}

type TerminalTeamsSession interface {
	OpenTerminalTeams(context.Context, TerminalTeamsRequest, Emit) error
}

type TerminalTeamsMountLifecycle interface {
	RetireTerminalTeams(string)
}

func (m *terminalModel) syncTerminalTeamsMount() {
	id := ""
	if p := m.state.LocalPanel; p != nil && p.props.Kind == "teams" {
		id = p.props.ID
	}
	if id == m.teamsMountID {
		return
	}
	if m.teamsMountID != "" {
		if source, ok := m.session.(TerminalTeamsMountLifecycle); ok {
			source.RetireTerminalTeams(m.teamsMountID)
		}
	}
	m.teamsMountID = id
}

func (m *terminalModel) terminalTeamsCommand() tea.Cmd {
	request := TerminalTeamsRequest{ID: uuid.NewString(), InitialTeams: cloneCachedTeams(m.state.CachedTeams), InitialFrame: m.state.CachedTeamsFrame}
	title := "Team"
	if len(request.InitialTeams) > 0 {
		title += " " + request.InitialTeams[0].String("name")
	}
	m.state.openLocalPanel(&LocalPanel{ID: request.ID, Kind: "teams", Title: title, HideDetails: true, Presentation: []byte(`{"level":"list","loading":true}`)})
	m.syncTerminalTeamsMount()
	return func() tea.Msg {
		if !m.beginTerminalUpdate() {
			return nil
		}
		defer m.turns.Done()
		source, ok := m.session.(TerminalTeamsSession)
		if !ok {
			return actionReturnedMsg{&UnsupportedAction{Action: "footer:teams"}}
		}
		err := source.OpenTerminalTeams(m.ctx, request, func(event Event) error { m.program.Send(actionEventMsg{event: event}); return nil })
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return actionReturnedMsg{err}
	}
}
