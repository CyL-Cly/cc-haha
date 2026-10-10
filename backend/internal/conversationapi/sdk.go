package conversationapi

import (
	"encoding/json"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// SDKCapture is the per-runtime Source reconnect/capture state. Reconnecting a
// socket retains this state; replacing its registered session resets it.
type SDKCapture struct {
	mu     sync.Mutex
	seen   map[string]bool
	order  []string
	recent []json.RawMessage
	bytes  int
	init   json.RawMessage
}

func NewSDKCapture() *SDKCapture { return &SDKCapture{seen: map[string]bool{}} }
func (s *SDKCapture) Parse(payload string) []json.RawMessage {
	result := []json.RawMessage{}
	for _, raw := range DecodeSDKPayload(payload) {
		if s.Observe(raw) {
			result = append(result, raw)
		}
	}
	return result
}

func DecodeSDKPayload(payload string) []json.RawMessage {
	result := []json.RawMessage{}
	for _, line := range strings.Split(payload, "\n") {
		line = trimSDK(line)
		if line == "" || !json.Valid([]byte(line)) {
			continue
		}
		raw := json.RawMessage(line)
		result = append(result, raw)
	}
	return result
}

func (s *SDKCapture) Seen(raw json.RawMessage) bool {
	var message protocol.JSONObject
	_ = message.UnmarshalJSON(raw)
	id := message.String("uuid")
	s.mu.Lock()
	defer s.mu.Unlock()
	return id != "" && s.seen[id]
}
func trimSDK(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r == '\ufeff' || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000
	})
}
func (s *SDKCapture) Observe(raw json.RawMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.remember(raw) {
		return false
	}
	var message protocol.JSONObject
	_ = message.UnmarshalJSON(raw)
	s.retain(raw, message)
	return true
}

// Remember reserves replay identity even when a late permission is rejected
// before Source retention. It is atomic across simultaneous SDK reconnects.
func (s *SDKCapture) Remember(raw json.RawMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remember(raw)
}
func (s *SDKCapture) remember(raw json.RawMessage) bool {
	var message protocol.JSONObject
	_ = message.UnmarshalJSON(raw)
	id := message.String("uuid")
	if id != "" {
		if s.seen[id] {
			return false
		}
		if len(s.order) >= 2000 {
			delete(s.seen, s.order[0])
			s.order = s.order[1:]
		}
		s.seen[id] = true
		s.order = append(s.order, id)
	}
	return true
}
func (s *SDKCapture) retain(raw json.RawMessage, message protocol.JSONObject) {
	retained := protocol.StringifyValidatedJavaScriptJSON(raw)
	if len(raw) > 64*1024 {
		retained = compactSDKMessage(raw, len(raw))
	}
	s.recent = append(s.recent, append(json.RawMessage(nil), retained...))
	s.bytes += len(retained)
	for len(s.recent) > 40 || s.bytes > 512*1024 {
		s.bytes -= len(s.recent[0])
		s.recent = s.recent[1:]
	}
	if message.String("type") == "system" && message.String("subtype") == "init" {
		s.init = append(json.RawMessage(nil), raw...)
	}
}
func (s *SDKCapture) Recent() []json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]json.RawMessage, len(s.recent))
	for i, v := range s.recent {
		out[i] = append(json.RawMessage(nil), v...)
	}
	return out
}
func (s *SDKCapture) Init() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(json.RawMessage(nil), s.init...)
}
func compactSDKMessage(raw json.RawMessage, size int) json.RawMessage {
	var message protocol.JSONObject
	if message.UnmarshalJSON(raw) != nil || !message.IsObject() {
		b, _ := obj(field("type", "unknown"), field("truncated", true), field("originalBytes", size)).MarshalJSON()
		return b
	}
	kind := message.String("type")
	if !isSDKString(message.Get("type")) {
		kind = "unknown"
	}
	out := obj(field("type", kind), field("truncated", true), field("originalBytes", size))
	for _, name := range []string{"subtype"} {
		if isSDKString(message.Get(name)) {
			out.Set(name, message.Get(name))
		}
	}
	for _, name := range []string{"is_error", "isApiErrorMessage"} {
		if raw := message.Get(name); string(raw) == "true" || string(raw) == "false" {
			out.Set(name, raw)
		}
	}
	for _, name := range []string{"status", "error", "result"} {
		if value, ok := truncateSDKText(message.Get(name)); ok {
			out.Set(name, value)
		}
	}
	var errors []json.RawMessage
	if json.Unmarshal(message.Get("errors"), &errors) == nil && string(message.Get("errors")) != "null" {
		values := []json.RawMessage{}
		for _, value := range errors[:min(5, len(errors))] {
			if text, ok := truncateSDKText(value); ok {
				values = append(values, text)
			}
		}
		b, _ := json.Marshal(values)
		out.Set("errors", b)
	}
	var api protocol.JSONObject
	_ = api.UnmarshalJSON(message.Get("message"))
	var blocks []protocol.JSONObject
	_ = json.Unmarshal(api.Get("content"), &blocks)
	var text json.RawMessage
	for _, block := range blocks {
		if block.String("type") == "text" && isSDKString(block.Get("text")) {
			text = block.Get("text")
			break
		}
	}
	var value string
	_ = json.Unmarshal(text, &value)
	if value != "" {
		text, _ = truncateSDKText(text)
		content, _ := json.Marshal([]protocol.JSONObject{obj(field("type", "text"), protocol.JSONField{Name: "text", Value: text})})
		out.Set("message", mustSDKObject(obj(protocol.JSONField{Name: "content", Value: content})))
	} else if text, ok := truncateSDKText(message.Get("message")); ok {
		out.Set("message", text)
	}
	return mustSDKObject(out)
}
func isSDKString(raw json.RawMessage) bool { return len(raw) > 0 && raw[0] == '"' }
func mustSDKObject(object protocol.JSONObject) json.RawMessage {
	b, _ := object.MarshalJSON()
	return b
}
func truncateSDKText(raw json.RawMessage) (json.RawMessage, bool) {
	if !isSDKString(raw) {
		return nil, false
	}
	units, e := protocol.DecodeJavaScriptJSONString(raw)
	if e != nil {
		return nil, false
	}
	text := string(utf16.Decode(units))
	encoded := []byte(text)
	if len(encoded) <= 4096 {
		return raw, true
	}
	// JS slices UTF-16 before encoding, then drops a trailing decoding replacement.
	if len(units) > 4096 {
		units = units[:4096]
	}
	prefix := []byte(string(utf16.Decode(units)))
	prefix = prefix[:min(4096, len(prefix))]
	decoded := string(protocol.DecodeJavaScriptUTF8(prefix))
	decoded = strings.TrimSuffix(decoded, "\ufffd")
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(decoded + "\n[truncated]"))), true
}
