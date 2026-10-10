// Package preflightcore is an ignored independent public-core candidate.
// It does not resolve full query preflight, active compaction, or host state.
package preflightcore

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "preflight candidate capability not migrated: " + e.Boundary
}
func capability(boundary string) error { return &CapabilityError{boundary} }

// SourceError preserves the independently reached pinned JSC exception class
// and literal message. A Go stack is not a JSC stack; callers must report that
// diagnostic boundary separately, rather than count this as strict full stdio.
type SourceError struct {
	Class       string `json:"class"`
	Name        string `json:"name"`
	Message     string `json:"message"`
	MessageJSON string `json:"messageJSON"`
}

func (e *SourceError) Error() string { return e.Name + ": " + e.Message }
func typeError(message string) error {
	raw, _ := json.Marshal(message)
	return &SourceError{"TypeError", "TypeError", message, string(raw)}
}

type record struct {
	Object                          protocol.JSONObject
	OriginalIndex                   int
	EnvelopeChanged, MessageChanged bool
}
type view struct {
	Records   []*record
	SameInput bool
}

func object(raw json.RawMessage) protocol.JSONObject {
	var value protocol.JSONObject
	_ = json.Unmarshal(raw, &value)
	return value
}
func property(raw json.RawMessage, name string) json.RawMessage { return object(raw).Get(name) }
func isNull(raw json.RawMessage) bool                           { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
func isArray(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '['
}
func isString(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '"'
}
func text(raw json.RawMessage) string {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return ""
	}
	return string(utf16.Decode(units))
}
func float(raw json.RawMessage) (float64, bool) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || isString(value) || isNull(value) || string(value) == "true" || string(value) == "false" || value[0] == '{' || value[0] == '[' {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(string(value), 64)
	return parsed, err == nil || math.IsInf(parsed, 0)
}
func truthy(raw json.RawMessage) bool {
	if len(raw) == 0 || isNull(raw) || string(bytes.TrimSpace(raw)) == "false" {
		return false
	}
	if isString(raw) {
		units, _ := protocol.DecodeJavaScriptJSONString(raw)
		return len(units) > 0
	}
	if value, ok := float(raw); ok {
		return value != 0 && !math.IsNaN(value)
	}
	return true
}
func stringify(raw json.RawMessage) (string, error) {
	value, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
	return string(value), err
}
func array(raw json.RawMessage) ([]json.RawMessage, error) {
	if !isArray(raw) {
		return nil, capability("original array")
	}
	var values []json.RawMessage
	err := json.Unmarshal(raw, &values)
	return values, err
}
func lastBoundary(history []*record) int {
	for index := len(history) - 1; index >= 0; index-- {
		r := history[index]
		if r.Object.String("type") == "system" && r.Object.String("subtype") == "compact_boundary" {
			return index
		}
	}
	return -1
}
func compact(history view) view {
	index := lastBoundary(history.Records)
	if index < 0 {
		return history
	}
	return view{append([]*record(nil), history.Records[index:]...), false}
}

var contextSuffix = regexp.MustCompile(`(?i)\[(1|2)m\]`)

func model(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", capability("unproved undefined model coercion")
	}
	if isNull(raw) {
		return "", typeError("null is not an object (evaluating 'modelInput.trim')")
	}
	if !isString(raw) {
		if _, numeric := float(raw); !numeric {
			return "", capability("unproved nonnumeric model trim/property error")
		}
		return "", typeError("modelInput.trim is not a function. (In 'modelInput.trim()', 'modelInput.trim' is undefined)")
	}
	units, _ := protocol.DecodeJavaScriptJSONString(raw)
	for _, unit := range units {
		if unit > 127 {
			return "", capability("unproved non-ASCII model coercion")
		}
	}
	value := strings.TrimSpace(text(raw))
	normalized := strings.ToLower(value)
	alias := strings.TrimSuffix(normalized, "[1m]")
	switch alias {
	case "opusplan", "fable", "sonnet", "haiku", "opus", "best":
		return "", capability("original model alias/provider/auth resolver")
	}
	return strings.ToLower(strings.TrimSpace(contextSuffix.ReplaceAllString(value, ""))), nil
}

func modelValue(raw json.RawMessage) (string, error) { return model(raw) }
func thinking(raw json.RawMessage) bool {
	kind := text(property(raw, "type"))
	return kind == "thinking" || kind == "redacted_thinking"
}
func signatures(history view, current json.RawMessage) (view, error) {
	return signaturesWithModel(history, current, model)
}

func signaturesWithModel(history view, current json.RawMessage, normalize func(json.RawMessage) (string, error)) (view, error) {
	var selected *record
	for index := len(history.Records) - 1; index >= 0; index-- {
		r := history.Records[index]
		if r.Object.String("type") != "assistant" {
			continue
		}
		api := object(r.Object.Get("message"))
		if api.String("model") == messages.SyntheticModel {
			continue
		}
		raw := api.Get("content")
		if isNull(raw) {
			return view{}, typeError("null is not an object (evaluating 'msg.message.content.some')")
		}
		if !isArray(raw) {
			return view{}, typeError("msg.message.content.some is not a function. (In 'msg.message.content.some(isThinkingBlock)', 'msg.message.content.some' is undefined)")
		}
		blocks, err := array(raw)
		if err != nil {
			return view{}, err
		}
		for _, block := range blocks {
			if isNull(block) {
				return view{}, capability("unproved null signature-block property error")
			}
			if thinking(block) {
				selected = r
				break
			}
		}
		if selected != nil {
			break
		}
	}
	if selected == nil {
		return history, nil
	}
	old := object(selected.Object.Get("message")).Get("model")
	if !truthy(old) {
		return history, nil
	}
	previous, err := normalize(old)
	if err != nil {
		return view{}, err
	}
	next, err := normalize(current)
	if err != nil {
		return view{}, err
	}
	if previous == next {
		return history, nil
	}
	output := append([]*record(nil), history.Records...)
	changed := false
	for index, r := range output {
		if r.Object.String("type") != "assistant" {
			continue
		}
		api := object(r.Object.Get("message"))
		content := api.Get("content")
		if !isArray(content) {
			continue
		}
		blocks, err := array(content)
		if err != nil {
			return view{}, err
		}
		kept := make([]json.RawMessage, 0, len(blocks))
		for _, block := range blocks {
			if isNull(block) {
				return view{}, capability("unproved null signature-block property error")
			}
			if !thinking(block) {
				kept = append(kept, block)
			}
		}
		if len(kept) == len(blocks) {
			continue
		}
		raw, _ := json.Marshal(kept)
		copyAPI := api.Clone()
		copyAPI.Set("content", raw)
		copyEnvelope := r.Object.Clone()
		newAPI, _ := copyAPI.MarshalJSON()
		copyEnvelope.Set("message", newAPI)
		output[index] = &record{copyEnvelope, r.OriginalIndex, true, true}
		changed = true
	}
	if changed {
		return view{output, false}, nil
	}
	return history, nil
}
