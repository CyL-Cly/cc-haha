package clibootstrap

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/goalstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) nativeGoalCommand(ctx context.Context, scope *cooperative.Scope, args string) (string, bool, error) {
	if !scope.BelongsTo(m.Owner) || m.options.Native == nil {
		return "", false, cooperative.ErrRestricted
	}
	parsed, err := goalstate.Parse(args)
	if err != nil {
		return err.Error(), false, nil
	}
	state, err := m.nativeGoalState(scope)
	if err != nil {
		return "", false, err
	}
	sid := m.Controller.SessionID()
	if parsed.Type == "clear" {
		existing, err := state.Get(scope, sid)
		if err != nil {
			return "", false, err
		}
		if existing == nil {
			history, err := m.Host.OriginalHistoryOnOwner(scope)
			if err != nil {
				return "", false, err
			}
			records := []protocol.JSONObject{}
			for _, message := range history {
				data, err := message.MarshalJSON()
				if err != nil {
					return "", false, err
				}
				var record protocol.JSONObject
				if err = record.UnmarshalJSON(data); err != nil {
					return "", false, err
				}
				records = append(records, record)
			}
			existing, err = state.Restore(scope, sid, records, m.options.Native.NowMillis())
			if err != nil {
				return "", false, err
			}
		}
		cleared, err := state.Clear(scope, sid)
		if err != nil {
			return "", false, err
		}
		if cleared != nil {
			existing = cleared
		}
		if existing != nil {
			return "Goal cleared: " + existing.Objective, false, nil
		}
		return "No active goal.", false, nil
	}
	disabled, managed, err := m.hook.PromptHooksDisabledOrManagedOnly()
	if err != nil {
		return "", false, err
	}
	if disabled {
		return "Cannot set /goal because hooks are disabled by policy settings.", false, nil
	}
	if managed {
		return "Cannot set /goal because only managed hooks are allowed.", false, nil
	}
	if m.options.ForegroundShellProgress {
		trusted, err := m.nativeGoalTrust(scope)
		if err != nil {
			return "", false, err
		}
		if !trusted {
			return "Cannot set /goal until this workspace is trusted.", false, nil
		}
	}
	goal, err := state.Set(scope, sid, parsed.Objective, m.options.Native.NowMillis())
	if err != nil {
		return "", false, err
	}
	return "Goal set: " + goal.Objective, true, context.Cause(ctx)
}

func (m *MainPrint) nativeGoalTrust(scope *cooperative.Scope) (bool, error) {
	if m.nativeGoals != nil && m.nativeGoals.trustAccepted {
		return true, nil
	}
	key, err := m.originalProjectConfigKey(scope)
	if err != nil {
		return false, err
	}
	global, err := m.Global.Get()
	if err != nil {
		return false, err
	}
	var projects protocol.JSONObject
	_ = projects.UnmarshalJSON(global.Get("projects"))
	check := func(path string) bool {
		var project protocol.JSONObject
		_ = project.UnmarshalJSON(projects.Get(strings.ReplaceAll(path, "\\", "/")))
		return planRawTruthy(project.Get("hasTrustDialogAccepted"))
	}
	trusted := check(key)
	for path := m.state.Snapshot().CWD; !trusted; path = filepath.Dir(path) {
		trusted = check(path)
		if filepath.Dir(path) == path {
			break
		}
	}
	if trusted && m.nativeGoals != nil {
		m.nativeGoals.trustAccepted = true
	}
	return trusted, nil
}
