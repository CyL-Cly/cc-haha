package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
)

// InferenceContextWindow reads the actual model bound to this ASK, including
// chained Skill overrides. A UI model change during ASK targets the next ASK.
func (h *Host) InferenceContextWindow(scope *cooperative.Scope) (float64, error) {
	if err := h.checkInferenceOwner(scope); err != nil {
		return 0, err
	}
	if h.options.Startup == nil || h.options.Global == nil {
		return 0, cap("actual Skill model context bindings")
	}
	model := h.options.Model
	if h.askRunning {
		model = h.askInferenceModel
	}
	model, err := h.skillMainModel(model)
	if err != nil {
		return 0, err
	}
	// The admitted native print host has the original undefined SDK beta state;
	// existing generic SDK beta input is not widened by this native getter.
	return h.ContextWindowForModel(scope, model)
}

func (h *Host) ContextWindowForModel(scope *cooperative.Scope, model string) (float64, error) {
	if err := h.checkInferenceOwner(scope); err != nil {
		return 0, err
	}
	if h.options.Startup == nil || h.options.Global == nil {
		return 0, cap("actual scoped model context bindings")
	}
	resolver := modelaccounting.RestoredModelResolver{Environment: h.options.OriginalEnv, Settings: h.options.Startup.GetInitialSettings, Global: h.options.Global.Get}
	return resolver.ContextWindow(model)
}
