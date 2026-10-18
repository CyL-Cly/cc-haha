package clibootstrap

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func cloneTerminalCachedTeams(teams []protocol.JSONObject) []protocol.JSONObject {
	result := make([]protocol.JSONObject, len(teams))
	for i, team := range teams {
		result[i] = team.Clone()
	}
	return result
}

func terminalCachedTeamsEqual(a, b []protocol.JSONObject) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func (m *MainPrint) terminalCachedTeams(scope *cooperative.Scope) ([]protocol.JSONObject, error) {
	enabled, err := m.terminalPaneTeamModeEnabled(scope)
	if err != nil || !enabled {
		return []protocol.JSONObject{}, err
	}
	roster, _ := m.teamState.snapshot.Object("teammates")
	count := 0
	for _, field := range roster.OwnProperties() {
		var member protocol.JSONObject
		_ = member.UnmarshalJSON(field.Value)
		if member.String("name") != "team-lead" {
			count++
		}
	}
	summary := protocol.NewJSONObject()
	summary.Set("name", m.teamState.snapshot.Get("teamName"))
	value, _ := json.Marshal(count)
	summary.Set("memberCount", value)
	summary.Set("runningCount", json.RawMessage(`0`))
	summary.Set("idleCount", json.RawMessage(`0`))
	return []protocol.JSONObject{summary}, nil
}

// The Source PromptInput reads this projection from its existing teamContext.
// It does not discover teams on disk or derive the roster from background jobs.
func (m *MainPrint) SubscribeTerminalCachedTeams(ctx context.Context, listener func([]protocol.JSONObject)) ([]protocol.JSONObject, func(), error) {
	var initial []protocol.JSONObject
	var id uint64
	var runtime *nativeTerminalTeamFooter
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		var err error
		initial, err = m.terminalCachedTeams(scope)
		if err != nil {
			return err
		}
		if m.nativeTerminalTeamFooter == nil {
			m.nativeTerminalTeamFooter = &nativeTerminalTeamFooter{listeners: map[uint64]func(bool){}}
		}
		runtime = m.nativeTerminalTeamFooter
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		if runtime.teamListeners == nil {
			runtime.teamListeners = map[uint64]func([]protocol.JSONObject){}
		}
		runtime.cachedTeams = cloneTerminalCachedTeams(initial)
		if listener != nil {
			runtime.sequence++
			id = runtime.sequence
			runtime.teamListeners[id] = listener
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	if err != nil {
		return nil, nil, err
	}
	var once sync.Once
	return cloneTerminalCachedTeams(initial), func() { once.Do(func() { runtime.mu.Lock(); delete(runtime.teamListeners, id); runtime.mu.Unlock() }) }, nil
}
