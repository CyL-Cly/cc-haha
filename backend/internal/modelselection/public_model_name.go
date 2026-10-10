package modelselection

import (
	"encoding/json"
	"slices"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

// PublicModelDisplayName follows Source getPublicModelDisplayName's exact
// provider ID comparisons. Canonical is deliberately unsuitable here: it
// classifies arbitrary ID substrings for accounting, rather than recognizing
// only the public IDs in getModelStrings. Unknown IDs have no display name.
func (s *Selector) PublicModelDisplayName(raw json.RawMessage) (string, error) {
	model, err := units(raw)
	if err != nil {
		return "", err
	}
	// Source's public display helper recognizes a narrower set than its full
	// Codex catalog. A catalog-only entry must still remain an unknown ID here.
	normalized, err := lower(trim(model))
	if err != nil {
		return "", err
	}
	switch ascii(normalized) {
	case "gpt-6", "gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna",
		"gpt-5.3-codex", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
		"gpt-5.3-codex-spark", "gpt-5.5", "gpt-5.4", "gpt-5.4-mini",
		"gpt-5.2", "gpt-5.2-codex", "gpt-5.1-codex", "gpt-5.1-codex-max", "gpt-5.1-codex-mini":
		return providerproxy.OpenAIModelDisplayName(ascii(normalized)), nil
	}
	var overrides protocol.JSONObject
	_ = overrides.UnmarshalJSON(s.options.Settings.Get("modelOverrides"))
	// Order matches the original switch, including duplicate override values.
	for _, entry := range []struct {
		firstParty, bedrock, vertex, foundry, label string
		oneM                                        bool
	}{
		{"claude-fable-5-1", "anthropic.claude-fable-5-1", "claude-fable-5-1", "claude-fable-5-1", "Fable 5.1", true},
		{"claude-fable-5", "anthropic.claude-fable-5", "claude-fable-5", "claude-fable-5", "Fable 5", true},
		{"claude-opus-5-5", "us.anthropic.claude-opus-5-5", "claude-opus-5-5", "claude-opus-5-5", "Opus 5.5", true},
		{"claude-opus-5", "anthropic.claude-opus-5", "claude-opus-5", "claude-opus-5", "Opus 5", true},
		{"claude-opus-4-8", "anthropic.claude-opus-4-8", "claude-opus-4-8", "claude-opus-4-8", "Opus 4.8", true},
		{"claude-opus-4-7", "us.anthropic.claude-opus-4-7-v1", "claude-opus-4-7", "claude-opus-4-7", "Opus 4.7", true},
		{"claude-opus-4-5-20251101", "us.anthropic.claude-opus-4-5-20251101-v1:0", "claude-opus-4-5@20251101", "claude-opus-4-5", "Opus 4.5", false},
		{"claude-opus-4-1-20250805", "us.anthropic.claude-opus-4-1-20250805-v1:0", "claude-opus-4-1@20250805", "claude-opus-4-1", "Opus 4.1", false},
		{"claude-opus-4-20250514", "us.anthropic.claude-opus-4-20250514-v1:0", "claude-opus-4@20250514", "claude-opus-4", "Opus 4", false},
		{"claude-sonnet-4-6", "us.anthropic.claude-sonnet-4-6", "claude-sonnet-4-6", "claude-sonnet-4-6", "Sonnet 4.6", true},
		{"claude-sonnet-5-5", "us.anthropic.claude-sonnet-5-5", "claude-sonnet-5-5", "claude-sonnet-5-5", "Sonnet 5.5", true},
		{"claude-sonnet-5", "anthropic.claude-sonnet-5", "claude-sonnet-5", "claude-sonnet-5", "Sonnet 5", true},
		{"claude-sonnet-4-5-20250929", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", "claude-sonnet-4-5@20250929", "claude-sonnet-4-5", "Sonnet 4.5", true},
		{"claude-sonnet-4-20250514", "us.anthropic.claude-sonnet-4-20250514-v1:0", "claude-sonnet-4@20250514", "claude-sonnet-4", "Sonnet 4", true},
		{"claude-3-7-sonnet-20250219", "us.anthropic.claude-3-7-sonnet-20250219-v1:0", "claude-3-7-sonnet@20250219", "claude-3-7-sonnet", "Sonnet 3.7", false},
		{"claude-3-5-sonnet-20241022", "anthropic.claude-3-5-sonnet-20241022-v2:0", "claude-3-5-sonnet-v2@20241022", "claude-3-5-sonnet", "Sonnet 3.5", false},
		{"claude-haiku-4-5-20251001", "us.anthropic.claude-haiku-4-5-20251001-v1:0", "claude-haiku-4-5@20251001", "claude-haiku-4-5", "Haiku 4.5", false},
		{"claude-3-5-haiku-20241022", "us.anthropic.claude-3-5-haiku-20241022-v1:0", "claude-3-5-haiku@20241022", "claude-3-5-haiku", "Haiku 3.5", false},
	} {
		id := entry.firstParty
		switch s.provider {
		case "bedrock":
			id = entry.bedrock
		case "vertex":
			id = entry.vertex
		case "foundry":
			id = entry.foundry
		}
		known := mustUnits(quote(id))
		if override := overrides.Get(entry.firstParty); override != nil {
			if value, e := units(override); e == nil && len(value) > 0 {
				known = value
			}
		}
		if slices.Equal(model, known) {
			return entry.label, nil
		}
		if entry.oneM && slices.Equal(model, append(known, '[', '1', 'm', ']')) {
			return entry.label + " (1M context)", nil
		}
	}
	return "", nil
}
