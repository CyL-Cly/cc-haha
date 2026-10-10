package preflightcore

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ModelInput contains original values. Nil is undefined, not JSON null.
// Alias/native credential resolution remains an explicit capability.
type ModelInput struct {
	Profile     string
	Original    json.RawMessage
	Environment map[string]string
}

type ProjectedRecord struct {
	Record          protocol.JSONObject `json:"record"`
	OriginalIndex   int                 `json:"originalIndex"`
	EnvelopeChanged bool                `json:"envelopeChanged"`
	MessageChanged  bool                `json:"messageChanged"`
}

type Projection struct {
	Records        []ProjectedRecord `json:"records"`
	SameInputArray bool              `json:"sameInputArray"`
}

func originalHistory(input []protocol.JSONObject) ([]*record, error) {
	result := make([]*record, 0, len(input))
	for index, value := range input {
		if !value.IsObject() {
			return nil, capability("raw scalar/null original-history decoder")
		}
		if _, err := value.MarshalJSON(); err != nil {
			return nil, capability("invalid original ordered JSON field")
		}
		if value.String("type") == "assistant" || value.String("type") == "user" {
			if !object(value.Get("message")).IsObject() {
				return nil, capability("unproved original message scalar/null property error")
			}
		}
		result = append(result, &record{Object: value, OriginalIndex: index})
	}
	return result, nil
}

func projection(value view) Projection {
	result := Projection{Records: make([]ProjectedRecord, 0, len(value.Records)), SameInputArray: value.SameInput}
	for _, r := range value.Records {
		result.Records = append(result.Records, ProjectedRecord{r.Object, r.OriginalIndex, r.EnvelopeChanged, r.MessageChanged})
	}
	return result
}

// LastCompactBoundaryIndex selects only actual system compact_boundary records.
func LastCompactBoundaryIndex(original []protocol.JSONObject) (int, error) {
	history, err := originalHistory(original)
	if err != nil {
		return -1, err
	}
	return lastBoundary(history), nil
}

// AfterCompactBoundary retains the last marker inclusively. Unchanged records
// share JSONObject state with the caller; caller owns concurrent mutation.
func AfterCompactBoundary(original []protocol.JSONObject) (Projection, error) {
	history, err := originalHistory(original)
	if err != nil {
		return Projection{}, err
	}
	return projection(compact(view{history, true})), nil
}

// StripProtectedSignatures makes new records only for actual protected blocks
// removed after the original model comparison. It never mutates the input.
func StripProtectedSignatures(original []protocol.JSONObject, model ModelInput) (Projection, error) {
	if model.Profile != "" && model.Profile != NativeDirectRelayProfile {
		return Projection{}, capability("unknown original signature model profile")
	}
	for name := range model.Environment {
		if model.Profile == NativeDirectRelayProfile && name == "ANTHROPIC_DEFAULT_FABLE_MODEL" {
			continue
		}
		switch name {
		case "CLAUDE_CODE_DISABLE_1M_CONTEXT", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL":
		default:
			return Projection{}, capability("unknown original model environment " + name)
		}
	}
	if len(model.Original) > 0 && !json.Valid(model.Original) {
		return Projection{}, capability("invalid original model JSON")
	}
	history, err := originalHistory(original)
	if err != nil {
		return Projection{}, err
	}
	normalize := func(raw json.RawMessage) (string, error) { return modelValue(raw) }
	if model.Profile == NativeDirectRelayProfile {
		normalize = func(raw json.RawMessage) (string, error) { return nativeSignatureModel(raw, model.Environment) }
	}
	result, err := signaturesWithModel(view{history, true}, model.Original, normalize)
	if err != nil {
		return Projection{}, err
	}
	return projection(result), nil
}

func RoughContent(original json.RawMessage) (float64, error) {
	if len(original) > 0 && !json.Valid(original) {
		return 0, capability("invalid original content JSON")
	}
	return roughContent(original)
}

func RoughHistory(original []protocol.JSONObject) (float64, error) {
	history, err := originalHistory(original)
	if err != nil {
		return 0, err
	}
	return roughHistory(history)
}

// EstimateContext deliberately does not call HasUsableContextUsage. Source's
// truthy usage guard and hasUsable's null property error occur at distinct calls.
func EstimateContext(original []protocol.JSONObject) (float64, error) {
	history, err := originalHistory(original)
	if err != nil {
		return 0, err
	}
	if err = validateUsageReads(history); err != nil {
		return 0, err
	}
	return tokenCount(history)
}

func HasUsableContextUsage(original []protocol.JSONObject) (bool, error) {
	history, err := originalHistory(original)
	if err != nil {
		return false, err
	}
	if err = validateUsageReads(history); err != nil {
		return false, err
	}
	return hasUsable(history)
}

// DoesMostRecentAssistantMessageExceed200k is the plan-mode runtime model
// observation. Source deliberately stops at the newest assistant, even when
// its usage is absent, invalid, synthetic or only reports output tokens.
func DoesMostRecentAssistantMessageExceed200k(original []protocol.JSONObject) (bool, error) {
	history, err := originalHistory(original)
	if err != nil {
		return false, err
	}
	for index := len(history) - 1; index >= 0; index-- {
		last := history[index]
		if last.Object.String("type") != "assistant" {
			continue
		}
		if err := validateUsageReads([]*record{last}); err != nil {
			return false, err
		}
		value := usage(last)
		if !truthy(value) {
			return false, nil
		}
		valid := validUsageShape(value)
		return valid && usageTotal(value) > 200000, nil
	}
	return false, nil
}

// Source's getTokenUsage reads content[0] before the synthetic-model condition.
// Unproved property failures and the two additional exact display sentinels
// remain caps rather than being accepted by a guessed usage/zero projection.
func validateUsageReads(history []*record) error {
	for _, r := range history {
		if r.Object.String("type") != "assistant" {
			continue
		}
		api := object(r.Object.Get("message"))
		if len(api.Get("usage")) == 0 {
			continue
		}
		content := api.Get("content")
		if len(content) == 0 || isNull(content) {
			return capability("unproved usage content null/undefined property error")
		}
		if isArray(content) {
			blocks, _ := array(content)
			if len(blocks) > 0 && text(property(blocks[0], "type")) == "text" {
				value := text(property(blocks[0], "text"))
				if value == "The user doesn't want to take this action right now. STOP what you are doing and wait for the user to tell you how to proceed." || value == "The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). STOP what you are doing and wait for the user to tell you how to proceed." {
					return capability("unproved additional synthetic display-text usage exclusion")
				}
			}
		}
	}
	return nil
}
