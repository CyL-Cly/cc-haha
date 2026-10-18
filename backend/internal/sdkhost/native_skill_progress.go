package sdkhost

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkoutput"
)

// SkillTool reports only normalized child tool messages. Retain this native
// producer in mutable history and pass it through the existing Source SDK
// normalizer; it is neither an Agent task nor a raw SDK progress event.
func retainedNativeSkillProgress(record protocol.JSONObject) bool {
	data, ok := record.Object("data")
	if record.String("type") != "progress" || !ok || data.String("type") != "skill_progress" || len(data.OwnProperties()) != 4 {
		return false
	}
	child := record.String("toolUseID")
	if !strings.HasPrefix(child, "skill_") || len(child) == len("skill_") {
		return false
	}
	if _, err := protocol.DecodeJavaScriptJSONString(record.Get("parentToolUseID")); err != nil {
		return false
	}
	for _, key := range []string{"prompt", "agentId"} {
		if _, err := protocol.DecodeJavaScriptJSONString(data.Get(key)); err != nil {
			return false
		}
	}
	if record.String("parentToolUseID") == "" || data.String("agentId") == "" {
		return false
	}
	nested, err := sdkoutput.Decode(data.Get("message"))
	if err != nil || nested.Record.String("type") != "assistant" && nested.Record.String("type") != "user" {
		return false
	}
	if len(nested.Content) != 1 {
		return false
	}
	for _, block := range nested.Content {
		if kind := block.Object.String("type"); kind == "tool_use" || kind == "tool_result" {
			return true
		}
	}
	return false
}

// NativeSkillProgressMessages is a narrow view of the actual parent Host's
// existing normalizer. It uses the bridge's active same-Owner scope, not a
// retained caller scope, and neither creates a factory nor grants tool rights.
func (h *Host) NativeSkillProgressMessages(scope *cooperative.Scope, original json.RawMessage) ([]protocol.JSONObject, error) {
	if h == nil || h.options.Print == nil || !h.options.Print.NativeProcess || h.normalizer == nil || !scope.BelongsTo(h.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	var result []protocol.JSONObject
	err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error {
		if h.closing {
			return cooperative.ErrClosed
		}
		message, err := sdkoutput.Decode(original)
		if err != nil {
			return err
		}
		if kind := message.Record.String("type"); kind != "assistant" && kind != "user" {
			return cooperative.ErrRestricted
		}
		normalized, err := h.normalizer.NormalizeMessages([]sdkoutput.Message{message})
		if err != nil {
			return err
		}
		result = make([]protocol.JSONObject, 0, len(normalized))
		for _, view := range normalized {
			bytes, err := view.MarshalJSON()
			if err != nil {
				return err
			}
			var record protocol.JSONObject
			if err := record.UnmarshalJSON(bytes); err != nil {
				return err
			}
			result = append(result, record)
		}
		return nil
	})
	return result, err
}
