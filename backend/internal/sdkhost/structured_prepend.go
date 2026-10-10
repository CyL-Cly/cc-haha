package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// PrependUserMessage retains Source's synthetic input shape and raw UTF16.
func (input *PlainStructuredInput) PrependUserMessage(scope *cooperative.Scope, content json.RawMessage) error {
	if scope == nil || !scope.BelongsTo(input.owner) {
		return cooperative.ErrRestricted
	}
	message := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"user"`)}, protocol.JSONField{Name: "session_id", Value: json.RawMessage(`""`)}, protocol.JSONField{Name: "message", Value: json.RawMessage(`{"role":"user","content":` + string(content) + `}`)}, protocol.JSONField{Name: "parent_tool_use_id", Value: json.RawMessage(`null`)})
	line, err := protocol.EncodeNDJSON(message)
	if err != nil {
		return err
	}
	input.prepended = append(input.prepended, string(line))
	return nil
}
