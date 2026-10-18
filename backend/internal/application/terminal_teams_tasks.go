package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func terminalTeamsDetailIdentity(member protocol.JSONObject) string {
	if !member.IsObject() {
		return ""
	}
	return string(protocol.StringifyValidatedJavaScriptJSON(member.Get("agentId"))) + ":" + string(protocol.StringifyValidatedJavaScriptJSON(member.Get("name")))
}

func (s *Session) startTerminalTeamsDetailTasks(ctx context.Context, state *terminalLocalPanelState, view *clibootstrap.NativeTerminalTeamsSnapshot, index int, revision uint64, emit tui.Emit) error {
	worker, job, err := s.registerTerminalTeamsJob(ctx, state.props.ID, true)
	if err != nil {
		return err
	}
	go func() {
		defer s.finishTerminalTeamsJob(job)
		tasks, err := view.Tasks(worker, index)
		state.mu.Lock()
		value := state.teamsPanel
		if worker.Err() != nil || value.detailRevision != revision || value.level != "detail" {
			state.mu.Unlock()
			return
		}
		if err == nil {
			value.tasks = tasks
		}
		s.refreshTerminalTeamsPanel(worker, state)
		panel := terminalPanelProjection(state.props)
		s.mu.Lock()
		active := !s.closed && s.terminalPanel == state
		s.mu.Unlock()
		state.mu.Unlock()
		if active && worker.Err() == nil && s.terminalTeamsMountLive(panel.ID) && emit != nil {
			_ = emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &panel, Background: true})
		}
	}()
	return nil
}
