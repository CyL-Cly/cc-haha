package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// Headless print creates AppState from getDefaultAppState, so mainLoopModel is
// null even when --model sets the inference override. The interactive initial
// state instead uses getInitialMainLoopModel (the admitted raw user setting).
func (m *MainPrint) nativeTeamLeaderModel(scope *cooperative.Scope) (string, error) {
	if !scope.BelongsTo(m.Owner) || m.nativeModelSelector == nil {
		return "", cooperative.ErrRestricted
	}
	if !m.options.Interactive {
		return "", nil
	}
	value, err := m.nativeModelSelector.UserSpecified()
	if err != nil {
		return "", err
	}
	if len(value) == 0 || string(value) == "null" || string(value) == `""` {
		return "", nil
	}
	return decodeNativeModel(value)
}
