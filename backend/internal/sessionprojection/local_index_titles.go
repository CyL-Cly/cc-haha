package sessionprojection

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// LocalIndexTitleText shares the original cleanup primitive. The caller owns
// localIndex's distinct title precedence and complete-line reduction semantics.
func LocalIndexTitleText(value json.RawMessage) json.RawMessage {
	units, ok := stringUnits(value)
	if !ok {
		return nil
	}
	return quoted(cleanTitle(units))
}
func LocalIndexUserTitle(content json.RawMessage) (json.RawMessage, error) { return userTitle(content) }
func LocalIndexGoalTitle(entry protocol.JSONObject) json.RawMessage        { return goalTitle(entry) }
func LocalIndexNormalizeDrive(value json.RawMessage, platform string) json.RawMessage {
	return normalizeDrive(value, platform)
}
