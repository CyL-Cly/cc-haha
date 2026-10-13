package querypreflight

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

// InspectionRecords projects the same live original envelopes as preflight.
// The owning Host must call this on its current scope, including during ASK.
func InspectionRecords(history []queryloop.Message) ([]protocol.JSONObject, error) {
	return records(history)
}

// EffectiveContextWindow reuses the native model/override/output reserve policy.
func EffectiveContextWindow(model string, environment map[string]string) float64 {
	effective, _ := nativeThresholds(model, environment)
	return effective
}

// ConfiguredContextWindow is the model window without proactive compact overrides.
func ConfiguredContextWindow(model string, environment map[string]string) float64 {
	window, _ := nativeContextWindow(model, environment)
	return window
}

// ContextWindowWithSDKBetas uses the same model/config/global authorities as
// model accounting. The beta state is an explicit process-scoped producer.
func (s *Service) ContextWindowWithSDKBetas(model string, env map[string]string, betas []string) (float64, error) {
	return (modelaccounting.RestoredModelResolver{Environment: env, Settings: s.dep.InitialSettings.GetInitialSettings, Global: s.dep.Global.Get, SDKBetas: betas}).ContextWindow(model)
}
func EffectiveContextWindowWithSDKBetas(model string, env map[string]string, window float64) float64 {
	_, known := nativeContextWindow(model, env)
	effective, _ := nativeThresholdsForWindow(model, env, window, known)
	return effective
}
