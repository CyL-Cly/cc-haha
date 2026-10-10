package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func backgroundUsageTruthy(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0
	default:
		return true
	}
}

// BackgroundFromTerminal ports SessionBackgroundHint's foreground branch.
// Automatic promotion never stamps the user's explicit-shortcut preference.
func (m *MainPrint) BackgroundFromTerminal(ctx context.Context) (bool, error) {
	var changed bool
	receipt, e := m.Owner.Submit(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if skillTruthy(m.options.Native.Environment["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"]) {
			return nil
		}
		has, e := m.Controller.HasForegroundTasksOnOwner(scope)
		if e != nil || !has {
			return e
		}
		changed, e = m.Controller.BackgroundForegroundTasksOnOwner(scope)
		if e != nil {
			return e
		}
		config, e := m.Global.Get()
		if e != nil {
			return e
		}
		if backgroundUsageTruthy(config.Get("hasUsedBackgroundTask")) {
			return nil
		}
		return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			if backgroundUsageTruthy(current.Get("hasUsedBackgroundTask")) {
				return current, nil
			}
			copy := current.Clone()
			copy.Set("hasUsedBackgroundTask", rawMain(true))
			return &copy, nil
		})
	})
	if e == nil {
		e = receipt.Wait()
	}
	return changed, e
}
