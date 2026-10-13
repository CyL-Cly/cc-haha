package claudecodehints

import (
	"encoding/json"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type GateState struct {
	Enabled, Disabled, Installed, Blocked bool
	Shown                                 []json.RawMessage
}

// Store belongs to one running CLI's owner. Server sessions never share a slot.
type Store struct {
	pending  *Hint
	shown    bool
	tried    map[string]bool
	sequence uint64
}

func (s *Store) Record(h Hint, gate GateState) bool {
	if !gate.Enabled || s.shown || gate.Disabled || len(gate.Shown) >= 100 {
		return false
	}
	name, market := Identifier(h.Value)
	if name == "" || market == "" || !Official(market) {
		return false
	}
	for _, id := range gate.Shown {
		if equalString(id, h.Value) {
			return false
		}
	}
	if gate.Installed || gate.Blocked {
		return false
	}
	key := string(h.Value)
	if s.tried == nil {
		s.tried = map[string]bool{}
	}
	if s.tried[key] {
		return false
	}
	s.tried[key] = true
	copy := Clone(h)
	s.pending = &copy
	s.sequence++
	return true
}
func (s *Store) Pending() (*Hint, uint64) {
	if s.pending == nil {
		return nil, s.sequence
	}
	copy := Clone(*s.pending)
	return &copy, s.sequence
}
func (s *Store) Clear(sequence uint64) {
	if sequence == s.sequence {
		s.pending = nil
	}
}
func (s *Store) MarkShown()  { s.shown = true }
func (s *Store) Shown() bool { return s.shown }
func equalString(a, b json.RawMessage) bool {
	x, e := protocol.DecodeJavaScriptJSONString(a)
	if e != nil {
		return false
	}
	y, e := protocol.DecodeJavaScriptJSONString(b)
	if e != nil || len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
func Identifier(raw json.RawMessage) (string, string) {
	u, e := protocol.DecodeJavaScriptJSONString(raw)
	if e != nil {
		return "", ""
	}
	pieces := strings.Split(string(utf16.Decode(u)), "@")
	if len(pieces) > 1 {
		return pieces[0], pieces[1]
	}
	return pieces[0], ""
}
func Official(market string) bool {
	switch strings.ToLower(market) {
	case "claude-code-marketplace", "claude-code-plugins", "claude-plugins-official", "anthropic-marketplace", "anthropic-plugins", "agent-skills", "life-sciences", "knowledge-work-plugins":
		return true
	}
	return false
}
func Truthy(raw json.RawMessage) bool {
	switch string(raw) {
	case "", "null", "false", "0", "-0":
		return false
	}
	if len(raw) > 0 && raw[0] == '"' {
		u, e := protocol.DecodeJavaScriptJSONString(raw)
		return e == nil && len(u) > 0
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return n != 0
	}
	return true
}

// The external build's GrowthBook client key is empty. Disk values therefore
// cannot enable a feature. The real Source ant overrides still take precedence.
func Enabled(environment map[string]string, global protocol.JSONObject) bool {
	if environment["USER_TYPE"] != "ant" {
		return false
	}
	var overrides protocol.JSONObject
	if overrides.UnmarshalJSON([]byte(environment["CLAUDE_INTERNAL_FC_OVERRIDES"])) == nil {
		if raw := overrides.Get("tengu_lapis_finch"); raw != nil {
			return Truthy(raw)
		}
	}
	var configOverrides protocol.JSONObject
	_ = configOverrides.UnmarshalJSON(global.Get("growthBookOverrides"))
	return Truthy(configOverrides.Get("tengu_lapis_finch"))
}
func StateFromGlobal(global protocol.JSONObject) GateState {
	var state protocol.JSONObject
	_ = state.UnmarshalJSON(global.Get("claudeCodeHints"))
	var shown []json.RawMessage
	_ = json.Unmarshal(state.Get("plugin"), &shown)
	return GateState{Disabled: Truthy(state.Get("disabled")), Shown: shown}
}

// Respond preserves all fields in old/future global config objects. Recording
// a response is show-once regardless of yes, no or disable.
func Respond(global protocol.JSONObject, id json.RawMessage, disable bool) protocol.JSONObject {
	copy := global.Clone()
	var state protocol.JSONObject
	_ = state.UnmarshalJSON(copy.Get("claudeCodeHints"))
	state = state.Clone()
	var shown []json.RawMessage
	_ = json.Unmarshal(state.Get("plugin"), &shown)
	if shown == nil {
		shown = []json.RawMessage{}
	}
	exists := false
	for _, old := range shown {
		if equalString(old, id) {
			exists = true
			break
		}
	}
	if !exists {
		shown = append(shown, append(json.RawMessage{}, id...))
		raw, _ := json.Marshal(shown)
		state.Set("plugin", raw)
	}
	if disable {
		state.Set("disabled", json.RawMessage(`true`))
	}
	raw, _ := state.MarshalJSON()
	copy.Set("claudeCodeHints", raw)
	return copy
}
