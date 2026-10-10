package modelselection

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"unicode/utf16"
)

// SDKCatalogModelID selects the actual shipped provider ID for the two menu
// entries distinct from defaultTier (Sonnet 4.5 versus selectable Sonnet 4.6).
// It shares the original modelOverrides precedence with the selector.
func (s *Selector) SDKCatalogModelID(canonical string) string {
	value := canonical
	if s.provider == "bedrock" {
		switch canonical {
		case "claude-sonnet-4-6":
			value = "us.anthropic.claude-sonnet-4-6"
		case "claude-opus-4-7":
			value = "us.anthropic.claude-opus-4-7-v1"
		}
	}
	var overrides protocol.JSONObject
	if overrides.UnmarshalJSON(s.options.Settings.Get("modelOverrides")) == nil {
		if raw := overrides.Get(canonical); raw != nil {
			if parsed, e := units(raw); e == nil && len(parsed) > 0 {
				return string(utf16.Decode(parsed))
			}
		}
	}
	return value
}
