package modelselection

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"unicode/utf16"
)

// ValidationFallbackID reads the three shipped original getModelStrings keys
// used by validateModel's 3P suggestion, with its existing override precedence.
func (s *Selector) ValidationFallbackID(canonical string) string {
	value := canonical
	switch s.provider {
	case "bedrock":
		switch canonical {
		case "claude-opus-4-1-20250805":
			value = "us.anthropic.claude-opus-4-1-20250805-v1:0"
		case "claude-sonnet-4-5-20250929":
			value = "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
		case "claude-sonnet-4-20250514":
			value = "us.anthropic.claude-sonnet-4-20250514-v1:0"
		}
	case "vertex":
		switch canonical {
		case "claude-opus-4-1-20250805":
			value = "claude-opus-4-1@20250805"
		case "claude-sonnet-4-5-20250929":
			value = "claude-sonnet-4-5@20250929"
		case "claude-sonnet-4-20250514":
			value = "claude-sonnet-4@20250514"
		}
	case "foundry":
		switch canonical {
		case "claude-opus-4-1-20250805":
			value = "claude-opus-4-1"
		case "claude-sonnet-4-5-20250929":
			value = "claude-sonnet-4-5"
		case "claude-sonnet-4-20250514":
			value = "claude-sonnet-4"
		}
	}
	var overrides protocol.JSONObject
	if overrides.UnmarshalJSON(s.options.Settings.Get("modelOverrides")) == nil {
		if raw := overrides.Get(canonical); raw != nil {
			if parsed, err := units(raw); err == nil && len(parsed) > 0 {
				return string(utf16.Decode(parsed))
			}
		}
	}
	return value
}
