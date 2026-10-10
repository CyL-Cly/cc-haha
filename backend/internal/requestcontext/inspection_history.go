package requestcontext

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// NormalizeForInspection delegates to the same real API message normalizer.
// Source's context analyser invokes it without a tool catalog or model override.
func NormalizeForInspection(history []protocol.JSONObject, dependencies Dependencies) ([]protocol.JSONObject, error) {
	return normalizeHistory(history, dependencies, nil)
}
