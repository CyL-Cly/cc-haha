package clibootstrap

import (
	"context"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type nativeTerminalTeamFooter struct {
	mu            sync.Mutex
	sequence      uint64
	current       bool
	listeners     map[uint64]func(bool)
	cachedTeams   []protocol.JSONObject
	teamListeners map[uint64]func([]protocol.JSONObject)
}

func (m *MainPrint) terminalPaneTeamEnabled(scope *cooperative.Scope) (bool, error) {
	enabled, err := m.terminalPaneTeamModeEnabled(scope)
	if err != nil || !enabled {
		return false, err
	}
	roster, _ := m.teamState.snapshot.Object("teammates")
	for _, field := range roster.OwnProperties() {
		var member protocol.JSONObject
		_ = member.UnmarshalJSON(field.Value)
		if member.String("name") != "team-lead" {
			return true, nil
		}
	}
	return false, nil
}

func (m *MainPrint) terminalPaneTeamModeEnabled(scope *cooperative.Scope) (bool, error) {
	enabled, err := m.nativeSendMessageEnabled(scope)
	if err != nil || !enabled || m.teamState == nil {
		return false, err
	}
	// Leader state is the actual launch mode snapshot. A restored team snapshot
	// may precede lazy TeamBindings setup, so use its same startup mode inputs.
	mode := m.options.Arguments.TeammateMode
	if m.teamLeader != nil {
		mode = m.teamLeader.mode
	} else if mode == "" {
		global, err := m.Global.Get()
		if err != nil {
			return false, err
		}
		mode = global.String("teammateMode")
	}
	if mode == "" {
		mode = "auto"
	}
	if !m.options.Interactive || mode == "in-process" {
		return false, nil
	}
	if m.teamLeader != nil {
		if m.teamLeader.panes.InProcessEnabled(mode, !m.options.Interactive) {
			return false, nil
		}
	} else if mode != "tmux" {
		env := m.options.Native.nativeEnvironmentSnapshot()
		if env["TMUX"] == "" && env["TERM_PROGRAM"] != "iTerm.app" && env["ITERM_SESSION_ID"] == "" {
			return false, nil
		}
	}
	return true, nil
}

func (m *MainPrint) SubscribeTerminalTeamFooter(ctx context.Context, listener func(bool)) (bool, func(), error) {
	var initial bool
	var id uint64
	var runtime *nativeTerminalTeamFooter
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		var err error
		initial, err = m.terminalPaneTeamEnabled(scope)
		if err != nil {
			return err
		}
		if m.nativeTerminalTeamFooter == nil {
			m.nativeTerminalTeamFooter = &nativeTerminalTeamFooter{current: initial, listeners: map[uint64]func(bool){}}
		}
		runtime = m.nativeTerminalTeamFooter
		runtime.mu.Lock()
		if listener != nil {
			runtime.sequence++
			id = runtime.sequence
			runtime.listeners[id] = listener
		}
		runtime.mu.Unlock()
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	if err != nil {
		return false, nil, err
	}
	var once sync.Once
	return initial, func() { once.Do(func() { runtime.mu.Lock(); delete(runtime.listeners, id); runtime.mu.Unlock() }) }, nil
}

func (m *MainPrint) publishTerminalTeamFooter(scope *cooperative.Scope) {
	// Source TeamCreate/TeamDelete notify the always-mounted tasks store
	// when its canonical list identity changes, independent of footer mounts.
	m.nativeTerminalTaskList.notify()
	runtime := m.nativeTerminalTeamFooter
	if runtime == nil {
		return
	}
	value, err := m.terminalPaneTeamEnabled(scope)
	if err != nil {
		return
	}
	runtime.mu.Lock()
	listeners := make([]func(bool), 0, len(runtime.listeners))
	if runtime.current != value {
		runtime.current = value
		for _, listener := range runtime.listeners {
			listeners = append(listeners, listener)
		}
	}
	teams, err := m.terminalCachedTeams(scope)
	teamListeners := []func([]protocol.JSONObject){}
	if err == nil && !terminalCachedTeamsEqual(runtime.cachedTeams, teams) {
		runtime.cachedTeams = cloneTerminalCachedTeams(teams)
		for _, listener := range runtime.teamListeners {
			teamListeners = append(teamListeners, listener)
		}
	}
	runtime.mu.Unlock()
	for _, listener := range listeners {
		listener(value)
	}
	for _, listener := range teamListeners {
		listener(cloneTerminalCachedTeams(teams))
	}
}
