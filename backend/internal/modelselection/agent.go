package modelselection

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// RuntimeModel is Source getRuntimeMainLoopModel. The caller supplies the
// actual query's token observation, rather than guessing it from model names.
func (s *Selector) RuntimeModel(parent json.RawMessage, permissionMode string, exceeds200kTokens bool) (json.RawMessage, error) {
	if _, err := units(parent); err != nil {
		return nil, err
	}
	specified, err := s.UserSpecified()
	if err != nil {
		return nil, err
	}
	if permissionMode == "plan" {
		if slices.Equal(mustUnits(specified), mustUnits(quote("opusplan"))) && !exceeds200kTokens {
			return append(json.RawMessage(nil), s.defaultTier("opus")...), nil
		}
		if slices.Equal(mustUnits(specified), mustUnits(quote("haiku"))) {
			return append(json.RawMessage(nil), s.defaultTier("sonnet")...), nil
		}
	}
	return append(json.RawMessage(nil), parent...), nil
}

type AgentInput struct {
	// Nil and JSON null both select the default agent definition (inherit).
	AgentModel, ParentModel, ToolSpecifiedModel json.RawMessage
	PermissionMode                              string
}

// AgentModel ports getAgentModel, including configured override precedence,
// exact parent-tier inheritance, and Bedrock cross-region profile inheritance.
func (s *Selector) AgentModel(in AgentInput) (json.RawMessage, error) {
	parent, err := units(in.ParentModel)
	if err != nil {
		return nil, err
	}
	configured := trim(mustUnits(quote(s.options.Environment["CLAUDE_CODE_SUBAGENT_MODEL"])))
	configuredLower, err := lower(configured)
	if err != nil {
		return nil, err
	}
	if len(configured) > 0 && !slices.Equal(configuredLower, mustUnits(quote("inherit"))) {
		return s.Parse(protocol.QuoteJavaScriptUTF16(configured))
	}
	model := in.AgentModel
	tool := false
	if in.ToolSpecifiedModel != nil && !bytes.Equal(bytes.TrimSpace(in.ToolSpecifiedModel), []byte("null")) {
		value, err := units(in.ToolSpecifiedModel)
		if err != nil {
			return nil, err
		}
		if len(value) > 0 {
			model, tool = in.ToolSpecifiedModel, true
		}
	}
	if model == nil || bytes.Equal(bytes.TrimSpace(model), []byte("null")) {
		model = quote("inherit")
	}
	value, err := units(model)
	if err != nil {
		return nil, err
	}
	if !tool && slices.Equal(value, mustUnits(quote("inherit"))) {
		return s.RuntimeModel(in.ParentModel, in.PermissionMode, false)
	}
	alias, err := lower(value)
	if err != nil {
		return nil, err
	}
	if isFamily(ascii(alias)) {
		canonical, err := s.canonical(in.ParentModel)
		if err != nil {
			return nil, err
		}
		if index(canonical, ascii(alias)) >= 0 {
			return append(json.RawMessage(nil), in.ParentModel...), nil
		}
	}
	resolved, err := s.Parse(model)
	if err != nil {
		return nil, err
	}
	prefix := bedrockRegionPrefix(parent)
	if prefix == "" || s.provider != "bedrock" || bedrockRegionPrefix(value) != "" {
		return resolved, nil
	}
	resolvedValue, err := units(resolved)
	if err != nil {
		return nil, err
	}
	if existing := bedrockRegionPrefix(resolvedValue); existing != "" {
		at := index(resolvedValue, existing+".")
		resolvedValue = append(append(append([]uint16(nil), resolvedValue[:at]...), mustUnits(quote(prefix+"."))...), resolvedValue[at+len(existing)+1:]...)
	} else if starts(resolvedValue, "anthropic.") {
		resolvedValue = append(mustUnits(quote(prefix+".")), resolvedValue...)
	}
	return protocol.QuoteJavaScriptUTF16(resolvedValue), nil
}

func starts(value []uint16, prefix string) bool { return index(value, prefix) == 0 }
func bedrockRegionPrefix(value []uint16) string {
	if starts(value, "arn:") {
		for at := len(value) - 1; at >= 0; at-- {
			if value[at] == '/' {
				value = value[at+1:]
				break
			}
		}
	}
	for _, prefix := range []string{"us", "eu", "apac", "global"} {
		if starts(value, prefix+".anthropic.") {
			return prefix
		}
	}
	return ""
}

func (s *Selector) canonical(raw json.RawMessage) ([]uint16, error) {
	value, err := units(s.reverse(raw))
	if err != nil {
		return nil, err
	}
	value, err = lower(value)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"claude-fable-5-1", "claude-fable-5", "claude-opus-5-5", "claude-opus-5", "claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-5", "claude-opus-4-1", "claude-opus-4", "claude-sonnet-5-5", "claude-sonnet-5", "claude-sonnet-4-6", "claude-sonnet-4-5", "claude-sonnet-4", "claude-haiku-4-5", "claude-3-7-sonnet", "claude-3-5-sonnet", "claude-3-5-haiku", "claude-3-opus", "claude-3-sonnet", "claude-3-haiku"} {
		if index(value, name) >= 0 {
			return mustUnits(quote(name)), nil
		}
	}
	// Source /(claude-(\d+-\d+-)?\w+)/ uses ASCII digit and word classes.
	for start := 0; start+7 <= len(value); start++ {
		if !starts(value[start:], "claude-") {
			continue
		}
		at := start + 7
		digitEnd := func(at int) int {
			for at < len(value) && value[at] >= '0' && value[at] <= '9' {
				at++
			}
			return at
		}
		first := digitEnd(at)
		if first > at && first < len(value) && value[first] == '-' {
			second := digitEnd(first + 1)
			if second > first+1 && second < len(value) && value[second] == '-' {
				at = second + 1
			}
		}
		end := at
		for end < len(value) && (value[end] >= 'a' && value[end] <= 'z' || value[end] >= 'A' && value[end] <= 'Z' || value[end] >= '0' && value[end] <= '9' || value[end] == '_') {
			end++
		}
		if end > at {
			return value[start:end], nil
		}
		// The optional version group can backtrack into the word group.
		if at != start+7 {
			return value[start:first], nil
		}
	}
	return value, nil
}

func (s *Selector) Canonical(raw json.RawMessage) (json.RawMessage, error) {
	value, err := s.canonical(raw)
	if err != nil {
		return nil, err
	}
	return protocol.QuoteJavaScriptUTF16(value), nil
}

// DefaultSetting retains the unparsed default used by Source cost fallback.
func (s *Selector) DefaultSetting() json.RawMessage {
	if s.provider == "azureOpenAI" {
		return quote("gpt-5.2-codex")
	}
	if s.openAIActive {
		return append(json.RawMessage(nil), s.defaultTier("opus")...)
	}
	if s.anthropicIdentity != nil {
		return append(json.RawMessage(nil), s.anthropicDefaultSetting(*s.anthropicIdentity)...)
	}
	if !s.thirdParty {
		return append(json.RawMessage(nil), s.defaultTier("opus")...)
	}
	return append(json.RawMessage(nil), s.defaultTier("sonnet")...)
}
