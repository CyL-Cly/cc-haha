package usercontext

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// RenderPrefix is the exact prependUserContext string recipe. Allocation of
// the meta-user envelope belongs to the caller's original model-call factory.
func RenderPrefix(value protocol.JSONObject) (json.RawMessage, error) {
	properties := value.OwnProperties()
	if len(properties) == 0 {
		return nil, nil
	}
	parts := []json.RawMessage{quote("<system-reminder>\nAs you answer the user's questions, you can use the following context:\n")}
	for index, property := range properties {
		if index > 0 {
			parts = append(parts, quote("\n"))
		}
		converted, err := protocol.ExactJavaScriptStringJSON(property.Value)
		if err != nil {
			return nil, err
		}
		parts = append(parts, quote("# "), property.NameJSON, quote("\n"), converted)
	}
	parts = append(parts, quote("\n\n      IMPORTANT: this context may or may not be relevant to your tasks. You should not respond to this context unless it is highly relevant to your task.\n</system-reminder>\n"))
	return concat(parts...), nil
}
