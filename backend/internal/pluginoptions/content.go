package pluginoptions

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// SubstituteUserConfigInContentJSON is the shared skill/agent prose consumer.
// Sensitive references never expose a stored value; missing references stay
// literal. Raw keys and results retain the original UTF-16 property identity.
func SubstituteUserConfigInContentJSON(text json.RawMessage, values, schema protocol.JSONObject) (json.RawMessage, error) {
	return protocol.ReplaceJavaScriptTemplatesJSON(text, "${user_config.", func(key json.RawMessage) (json.RawMessage, error) {
		var declaration protocol.JSONObject
		raw, err := schema.GetPropertyJSON(key)
		if err != nil {
			return nil, err
		}
		if declaration.UnmarshalJSON(raw) == nil && string(declaration.Get("sensitive")) == "true" {
			units, _ := protocol.DecodeJavaScriptJSONString(key)
			prefix, _ := protocol.DecodeJavaScriptJSONString(json.RawMessage(`"[sensitive option '"`))
			suffix, _ := protocol.DecodeJavaScriptJSONString(json.RawMessage(`"' not available in skill content]"`))
			return protocol.QuoteJavaScriptUTF16(append(append(prefix, units...), suffix...)), nil
		}
		return values.GetOrdinaryPropertyStringJSON(key)
	})
}
