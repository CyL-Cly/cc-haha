package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This initial value is issued only by the real cached Footer subscription.
// It retains the already mounted component's initial team across a later
// parent teamContext update. No JSON request or availability flag can issue it.
type NativeTerminalTeamsInitial struct {
	main  *MainPrint
	teams []protocol.JSONObject
}

func (initial *NativeTerminalTeamsInitial) InitialTeams() []protocol.JSONObject {
	if initial == nil {
		return []protocol.JSONObject{}
	}
	return cloneTerminalCachedTeams(initial.teams)
}

func (initial *NativeTerminalTeamsInitial) OwnsMain(main *MainPrint) bool {
	return initial != nil && main != nil && initial.main == main && len(initial.teams) > 0
}

func (m *MainPrint) nativeTerminalTeamsRoot(scope *cooperative.Scope) bool {
	return m != nil && scope.BelongsTo(m.Owner) && m.options.Native != nil && m.options.Native.NativeProcess && m.options.Interactive && m.initialized && !m.closed && !m.shuttingDown && !m.agentChild && m.teamWorker == nil && m.Controller != nil && m.Controller.BootstrapOwns(m.Owner) && m.taskStore != nil
}

func (m *MainPrint) SubscribeNativeTerminalTeamsInitial(ctx context.Context, listener func(*NativeTerminalTeamsInitial)) (*NativeTerminalTeamsInitial, func(), error) {
	root := false
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if !scope.BelongsTo(m.Owner) || m.options.Native == nil || !m.options.Native.NativeProcess || !m.options.Interactive || !m.initialized || m.closed || m.shuttingDown || m.Controller == nil || !m.Controller.BootstrapOwns(m.Owner) {
			return cooperative.ErrRestricted
		}
		root = m.nativeTerminalTeamsRoot(scope)
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	if err != nil {
		return nil, nil, err
	}
	if !root {
		// Actual pane/agent actors have no root Footer team claim. Their TUI
		// still subscribes to the optional projection; absence is empty data,
		// not a fatal startup error or authority to open the leader's dialog.
		return nil, func() {}, nil
	}
	issue := func(teams []protocol.JSONObject) *NativeTerminalTeamsInitial {
		if len(teams) == 0 {
			return nil
		}
		return &NativeTerminalTeamsInitial{main: m, teams: cloneTerminalCachedTeams(teams)}
	}
	teams, remove, err := m.SubscribeTerminalCachedTeams(ctx, func(teams []protocol.JSONObject) {
		if listener != nil {
			listener(issue(teams))
		}
	})
	if err != nil {
		return nil, nil, err
	}
	return issue(teams), remove, nil
}

func (m *MainPrint) OpenNativeTerminalTeamsInitial(ctx context.Context, initial *NativeTerminalTeamsInitial) (*NativeTerminalTeams, error) {
	if initial == nil || initial.main != m || len(initial.teams) == 0 {
		return nil, cooperative.ErrRestricted
	}
	return m.openNativeTerminalTeams(ctx, initial.InitialTeams(), initial)
}
