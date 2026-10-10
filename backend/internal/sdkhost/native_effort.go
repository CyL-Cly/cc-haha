package sdkhost

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// Interactive /effort updates AppState's value and clears the CLI-only effort
// precedence. Source environment overrides continue to win after this command.
func (h *Host) SetNativeTerminalEffort(scope *cooperative.Scope, value json.RawMessage) error {
	model, err := h.InferenceModel(scope)
	if err != nil {
		return err
	}
	if err = h.SetInferenceModel(scope, model, value, true); err != nil {
		return err
	}
	h.options.Print.EffortValueOverridesEnv = false
	return nil
}
