package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) OpenTerminalTeams(ctx context.Context, request tui.TerminalTeamsRequest, emit tui.Emit) error {
	if !s.terminalInteractive || request.ID == "" {
		return fmt.Errorf("Teams Footer unavailable")
	}
	var captured *clibootstrap.NativeTerminalTeamsInitial
	if request.InitialFrame != nil {
		var ok bool
		captured, ok = request.InitialFrame.(*clibootstrap.NativeTerminalTeamsInitial)
		if !ok || !captured.OwnsMain(s.process) || !bytes.Equal(raw(request.InitialTeams), raw(captured.InitialTeams())) {
			return cooperative.ErrRestricted
		}
	}
	worker, job, err := s.registerTerminalTeamsJob(ctx, request.ID, true)
	if err != nil {
		return err
	}
	state := &terminalLocalPanelState{props: tui.LocalPanel{ID: request.ID, Kind: "teams", HideDetails: true}, rows: map[string]protocol.JSONObject{}, teamsPanel: &terminalTeamsPanelState{loading: true, level: "list", mount: job}}
	if len(request.InitialTeams) > 0 {
		state.teamsPanel.teamName = request.InitialTeams[0].String("name")
	}
	s.refreshTerminalTeamsPanel(ctx, state)
	runtime := s.terminalTeams
	runtime.mu.Lock()
	if runtime.closed || runtime.retired[request.ID] || worker.Err() != nil {
		runtime.mu.Unlock()
		s.finishTerminalTeamsJob(job)
		return context.Canceled
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		runtime.mu.Unlock()
		s.finishTerminalTeamsJob(job)
		return context.Canceled
	}
	previous := s.terminalPanel
	s.terminalPanel = state
	s.mu.Unlock()
	runtime.mu.Unlock()
	s.closeTerminalUsagePanel(previous)
	s.closeTerminalMCPAuthPanel(previous)
	s.closeTerminalBtwPanel(previous)
	s.closeTerminalTeamsPanel(previous)
	go func() {
		defer s.finishTerminalTeamsJob(job)
		var native *clibootstrap.NativeTerminalTeams
		var err error
		if captured != nil {
			native, err = s.process.OpenNativeTerminalTeamsInitial(worker, captured)
		} else {
			native, err = s.process.OpenNativeTerminalTeams(worker, request.InitialTeams)
		}
		state.mu.Lock()
		state.teamsPanel.native = native
		state.mu.Unlock()
		if err != nil {
			s.mergeTerminalTeamsSnapshot(worker, state, nil, err, emit)
			return
		}
		for {
			value, err := native.CaptureSnapshot(worker)
			s.mergeTerminalTeamsSnapshot(worker, state, value, err, emit)
			if worker.Err() != nil {
				return
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-worker.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return nil
}
func (s *Session) refreshTerminalTeamsPanel(_ context.Context, state *terminalLocalPanelState) error {
	value := state.teamsPanel
	if value == nil {
		return nil
	}
	state.props.Title = "Team " + value.teamName
	state.props.Description = ""
	state.props.Rows = nil
	state.props.Fields = nil
	state.props.Actions = []tui.LocalPanelAction{}
	state.props.DismissAction = "close"
	old := state.rows
	state.rows = map[string]protocol.JSONObject{}
	value.memberIndices = map[string]int{}
	for index, member := range value.members {
		rowID := ""
		for id, prior := range old {
			if _, used := state.rows[id]; !used && prior.String("agentId") == member.String("agentId") && prior.String("name") == member.String("name") {
				rowID = id
				break
			}
		}
		if rowID == "" {
			rowID = newID()
		}
		state.rows[rowID] = member.Clone()
		value.memberIndices[rowID] = index
		state.props.Rows = append(state.props.Rows, tui.LocalPanelRow{ID: rowID, Label: "@" + member.String("name"), Detail: member.String("status"), Actions: []tui.LocalPanelAction{{Key: "enter", Name: "teams-detail", Label: "view"}}})
		if value.level == "detail" && member.String("name") == value.memberName {
			state.selected = rowID
			state.props.SelectedRow = rowID
		}
	}
	state.props.Presentation = raw(map[string]any{"level": value.level, "teamName": value.teamName, "members": value.members, "tasks": value.tasks, "memberName": value.memberName, "supportsHideShow": value.supportsHideShow, "loading": value.loading, "error": value.err, "selectionRevision": value.selectionRevision, "selectionMaximum": value.selectionMaximum})
	state.props.TeamsFrame = nil
	if frame := s.captureTerminalTeamsFrame(state); frame != nil {
		state.props.TeamsFrame = frame
	}
	return nil
}
func (s *Session) mergeTerminalTeamsSnapshot(ctx context.Context, state *terminalLocalPanelState, snapshot *clibootstrap.NativeTerminalTeamsSnapshot, err error, emit tui.Emit) {
	if ctx.Err() != nil {
		return
	}
	state.mu.Lock()
	data := state.teamsPanel
	taskIndex, taskRevision := -1, uint64(0)
	data.loading = false
	if err != nil {
		data.err = err.Error()
	} else {
		value := snapshot.Projection()
		data.snapshot = snapshot
		data.err = ""
		data.teamName = value.String("teamName")
		data.supportsHideShow = string(value.Get("supportsHideShow")) == "true"
		_ = json.Unmarshal(value.Get("members"), &data.members)
		if data.level == "detail" {
			identity := ""
			for index, member := range data.members {
				if member.String("name") == data.memberName {
					identity, taskIndex = terminalTeamsDetailIdentity(member), index
					break
				}
			}
			if identity != data.detailIdentity {
				data.detailIdentity = identity
				data.detailRevision++
				if identity == "" {
					// The original detail component unmounts while its member is
					// missing. A later matching member mounts with empty tasks.
					data.tasks = nil
				} else {
					taskRevision = data.detailRevision
				}
			}
		}
	}
	s.refreshTerminalTeamsPanel(ctx, state)
	panel := terminalPanelProjection(state.props)
	s.mu.Lock()
	active := !s.closed && s.terminalPanel == state
	s.mu.Unlock()
	state.mu.Unlock()
	if active && ctx.Err() == nil && s.terminalTeamsMountLive(panel.ID) && emit != nil {
		_ = emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &panel, Background: true})
	}
	if active && taskRevision != 0 {
		_ = s.startTerminalTeamsDetailTasks(ctx, state, snapshot, taskIndex, taskRevision, emit)
	}
}

func (s *Session) applyTerminalTeamsPanel(ctx context.Context, result tui.LocalPanelResult, emit tui.Emit) (bool, error) {
	s.mu.Lock()
	state := s.terminalPanel
	s.mu.Unlock()
	if state == nil {
		return false, nil
	}
	state.mu.Lock()
	if state.props.Kind != "teams" || state.props.ID != result.ID || state.teamsPanel == nil {
		state.mu.Unlock()
		return false, nil
	}
	value := state.teamsPanel
	if result.Action == "close" {
		state.mu.Unlock()
		s.closeTerminalTeamsPanel(state)
		s.mu.Lock()
		if s.terminalPanel == state {
			s.terminalPanel = nil
		}
		s.mu.Unlock()
		if emit != nil {
			return true, emit(tui.Event{Kind: tui.LocalPanelClosed, LocalPanel: &tui.LocalPanel{ID: state.props.ID, Kind: "teams"}})
		}
		return true, nil
	}
	rowID := result.Row
	frame, ok := result.TeamsFrame.(*terminalTeamsFrame)
	if !ok || frame == nil || frame.id != state.props.ID || !frame.view.OwnsDialog(value.native) || result.TeamsLevel != frame.level {
		state.mu.Unlock()
		return true, cooperative.ErrRestricted
	}
	if !s.terminalTeamsMountLive(result.ID) {
		state.mu.Unlock()
		return true, context.Canceled
	}
	index, exists := frame.rows[rowID]
	member := protocol.JSONObject{}
	if exists {
		if result.TeamsIndex != index {
			state.mu.Unlock()
			return true, cooperative.ErrRestricted
		}
		member = frame.members[index].Clone()
	}
	if result.Action == "teams-back" {
		value.level = "list"
		value.memberName = ""
		value.tasks = nil
		value.detailIdentity = ""
		value.detailRevision++
		state.selected = ""
		state.props.SelectedRow = ""
		s.refreshTerminalTeamsPanel(ctx, state)
		panel := terminalPanelProjection(state.props)
		state.mu.Unlock()
		if emit != nil {
			return true, emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &panel})
		}
		return true, nil
	}
	if value.loading || value.native == nil || value.snapshot == nil {
		state.mu.Unlock()
		return true, nil
	}
	if result.Action == "teams-detail" {
		if !member.IsObject() {
			state.mu.Unlock()
			return true, nil
		}
		value.level = "detail"
		value.memberName = member.String("name")
		value.detailIdentity = terminalTeamsDetailIdentity(member)
		value.tasks = []protocol.JSONObject{}
		value.detailRevision++
		revision := value.detailRevision
		state.selected = rowID
		view := frame.view
		s.refreshTerminalTeamsPanel(ctx, state)
		panel := terminalPanelProjection(state.props)
		state.mu.Unlock()
		if emit != nil {
			if err := emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &panel}); err != nil {
				return true, err
			}
		}
		return true, s.startTerminalTeamsDetailTasks(ctx, state, view, index, revision, emit)
	}
	memberID := member.String("agentId")
	if frame.level != "list" && (result.Action == "teams-prune-idle" || result.Action == "teams-hide-all-toggle") {
		state.mu.Unlock()
		return true, nil
	}
	if result.Action == "teams-prune-idle" || result.Action == "teams-hide-all-toggle" || result.Action == "teams-cycle-mode" && frame.level == "list" {
		memberID = ""
	} else if !member.IsObject() {
		state.mu.Unlock()
		return true, nil
	}
	switch result.Action {
	case "teams-view", "teams-kill", "teams-shutdown", "teams-hide-toggle", "teams-hide-all-toggle", "teams-prune-idle", "teams-cycle-mode":
	default:
		state.mu.Unlock()
		return true, fmt.Errorf("Unknown Teams panel action")
	}
	if (result.Action == "teams-hide-toggle" || result.Action == "teams-hide-all-toggle") && !value.supportsHideShow {
		state.mu.Unlock()
		return true, nil
	}
	if frame.level == "list" {
		if len(frame.members) == 0 && (result.Action == "teams-cycle-mode" || result.Action == "teams-hide-all-toggle") {
			state.mu.Unlock()
			return true, nil
		}
		if result.Action == "teams-prune-idle" {
			hasIdle := false
			for _, row := range frame.members {
				if row.String("status") == "idle" {
					hasIdle = true
					break
				}
			}
			if !hasIdle {
				state.mu.Unlock()
				return true, nil
			}
		}
	}
	index = -1
	if memberID != "" {
		index = frame.rows[rowID]
	}
	action := frame.view.CaptureAction(result.Action, index)
	wasDetail := frame.level == "detail"
	selectionMaximum := len(frame.members) - 2
	if result.Action == "teams-prune-idle" {
		idle := 0
		for _, row := range frame.members {
			if row.String("status") == "idle" {
				idle++
			}
		}
		selectionMaximum = len(frame.members) - idle - 1
	}
	if selectionMaximum < 0 {
		selectionMaximum = 0
	}
	if wasDetail && (result.Action == "teams-shutdown" || result.Action == "teams-hide-toggle") {
		value.level = "list"
		value.memberName = ""
		value.tasks = nil
		value.detailIdentity = ""
		value.detailRevision++
	}
	state.mu.Unlock()
	worker, job, err := s.registerTerminalTeamsJob(context.WithoutCancel(ctx), result.ID, false)
	if err != nil {
		return true, err
	}
	if result.Action == "teams-view" {
		s.closeTerminalTeamsPanel(state)
		s.mu.Lock()
		if s.terminalPanel == state {
			s.terminalPanel = nil
		}
		s.mu.Unlock()
		if emit != nil {
			_ = emit(tui.Event{Kind: tui.LocalPanelClosed, LocalPanel: &tui.LocalPanel{ID: state.props.ID, Kind: "teams"}})
		}
	}
	go func() {
		defer s.finishTerminalTeamsJob(job)
		err := action.Run(worker)
		if result.Action == "teams-view" || worker.Err() != nil {
			return
		}
		state.mu.Lock()
		if err == nil && !wasDetail && (result.Action == "teams-kill" || result.Action == "teams-prune-idle") {
			value.selectionRevision++
			value.selectionMaximum = selectionMaximum
		}
		if err == nil && wasDetail && result.Action == "teams-kill" {
			value.level = "list"
			value.memberName = ""
			value.tasks = nil
			value.detailIdentity = ""
			value.detailRevision++
		}
		state.mu.Unlock()
		if err != nil {
			return
		}
		snapshot, err := value.native.CaptureSnapshot(worker)
		s.mergeTerminalTeamsSnapshot(worker, state, snapshot, err, emit)
	}()
	return true, nil
}
