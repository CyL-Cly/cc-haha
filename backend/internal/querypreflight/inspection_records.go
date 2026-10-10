package querypreflight

import (
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
