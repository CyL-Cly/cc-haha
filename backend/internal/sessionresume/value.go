// Package sessionresume restores persisted Agent envelopes before a new turn.
// It is distinct from desktop display projection and API history normalization.
package sessionresume

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Message retains ordered unknown fields and object identity through reduction.
// Callers keep these pointers when they need the interruption record identity.
type Message struct{ value *value }

type value struct {
	raw        json.RawMessage
	object     protocol.JSONObject
	properties map[string]*value
	items      []*value
	isArray    bool
}

func parse(raw json.RawMessage) (*value, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("resume message requires valid JSON")
	}
	item := &value{raw: append(json.RawMessage(nil), bytes.TrimSpace(raw)...)}
	if item.raw[0] == '{' {
		if err := json.Unmarshal(item.raw, &item.object); err != nil {
			return nil, err
		}
		item.properties = map[string]*value{}
	} else if item.raw[0] == '[' {
		var values []json.RawMessage
		if err := json.Unmarshal(item.raw, &values); err != nil {
			return nil, err
		}
		item.isArray = true
		item.items = make([]*value, 0, len(values))
		for _, raw := range values {
			child, err := parse(raw)
			if err != nil {
				return nil, err
			}
			item.items = append(item.items, child)
		}
	}
	return item, nil
}

func (item *value) get(name string) *value {
	if item == nil || !item.object.IsObject() {
		return nil
	}
	if child, exists := item.properties[name]; exists {
		return child
	}
	raw := item.object.Get(name)
	if raw == nil {
		return nil
	}
	child, _ := parse(raw)
	item.properties[name] = child
	return child
}
func (item *value) set(name string, child *value) {
	item.properties[name] = child
	item.object.Set(name, encoded(child))
}
func encoded(item *value) json.RawMessage {
	if item == nil {
		return nil
	}
	if item.object.IsObject() {
		for name, child := range item.properties {
			item.object.Set(name, encoded(child))
		}
		raw, _ := item.object.MarshalJSON()
		return raw
	}
	if item.isArray {
		raws := make([]json.RawMessage, 0, len(item.items))
		for _, child := range item.items {
			raw := encoded(child)
			if raw == nil {
				raw = json.RawMessage("null")
			}
			raws = append(raws, raw)
		}
		raw, _ := json.Marshal(raws)
		return raw
	}
	return append(json.RawMessage(nil), item.raw...)
}
func clone(item *value) *value { copy, _ := parse(encoded(item)); return copy }
func quoted(text string) *value {
	item, _ := parse(protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(text))))
	return item
}
func stringValue(item *value) bool { return item != nil && len(item.raw) > 0 && item.raw[0] == '"' }
func text(item *value) string {
	if !stringValue(item) {
		return ""
	}
	var result string
	_ = json.Unmarshal(item.raw, &result)
	return result
}
func null(item *value) bool { return item != nil && bytes.Equal(item.raw, []byte("null")) }
func truthy(item *value) bool {
	if item == nil || null(item) || bytes.Equal(item.raw, []byte("false")) {
		return false
	}
	if stringValue(item) {
		units, _ := protocol.DecodeJavaScriptJSONString(item.raw)
		return len(units) > 0
	}
	if item.object.IsObject() || item.isArray || bytes.Equal(item.raw, []byte("true")) {
		return true
	}
	number, _ := strconv.ParseFloat(string(item.raw), 64)
	return number != 0 && !math.IsNaN(number)
}
func identity(item *value) string {
	if item == nil {
		return "undefined"
	}
	if stringValue(item) {
		units, _ := protocol.DecodeJavaScriptJSONString(item.raw)
		return "string:" + string(protocol.QuoteJavaScriptUTF16(units))
	}
	if item.object.IsObject() || item.isArray {
		return fmt.Sprintf("object:%p", item)
	}
	if null(item) || bytes.Equal(item.raw, []byte("true")) || bytes.Equal(item.raw, []byte("false")) {
		return string(item.raw)
	}
	number, _ := strconv.ParseFloat(string(item.raw), 64)
	if number == 0 {
		number = 0
	}
	return "number:" + strconv.FormatUint(math.Float64bits(number), 16)
}
func same(left, right *value) bool { return identity(left) == identity(right) }

// Native diagnostic string rendering differs from JSON.stringify: string
// contents are quoted without escaping, and UTF-16 units stay intact.
func nativeDescription(item *value) json.RawMessage {
	if item == nil {
		return json.RawMessage(`"undefined"`)
	}
	if stringValue(item) {
		units, _ := protocol.DecodeJavaScriptJSONString(item.raw)
		return protocol.QuoteJavaScriptUTF16(append(append([]uint16{'"'}, units...), '"'))
	}
	if item.object.IsObject() {
		return json.RawMessage(`"an instance of Object"`)
	}
	if item.isArray {
		return json.RawMessage(`"an instance of Array"`)
	}
	raw, _ := protocol.JavaScriptStringJSON(item.raw)
	return raw
}
func typeErrorValue(prefix string, item *value, suffix string) error {
	units, _ := protocol.DecodeJavaScriptJSONString(nativeDescription(item))
	joined := append(utf16.Encode([]rune(prefix)), units...)
	joined = append(joined, utf16.Encode([]rune(suffix))...)
	raw := protocol.QuoteJavaScriptUTF16(joined)
	var readable string
	_ = json.Unmarshal(raw, &readable)
	return &Failure{Name: "TypeError", Message: readable, MessageJSON: raw}
}

func ParseMessages(raws []json.RawMessage) ([]*Message, error) {
	result := make([]*Message, 0, len(raws))
	for _, raw := range raws {
		item, err := parse(raw)
		if err != nil {
			return nil, err
		}
		result = append(result, &Message{item})
	}
	return result, nil
}
func (message *Message) MarshalJSON() ([]byte, error)      { return encoded(message.value), nil }
func (message *Message) UUID() json.RawMessage             { return encoded(message.value.get("uuid")) }
func (message *Message) Field(name string) json.RawMessage { return encoded(message.value.get(name)) }

func (message *Message) HasOwnField(name string) bool {
	_, exists := message.value.properties[name]
	return exists || message.value.object.Get(name) != nil
}

// Failure is an observed Source exception at a specific reducer operation.
// Native Go diagnostic stacks are recorded separately by the caller.
type Failure struct {
	Name        string          `json:"name"`
	Message     string          `json:"message"`
	MessageJSON json.RawMessage `json:"-"`
}

// MessageValue preserves raw Source UTF-16 diagnostics for JSON observers.
// Error() remains a readable native error; its stack is a separate artifact.
func (failure *Failure) MessageValue() json.RawMessage {
	if failure.MessageJSON != nil {
		return append(json.RawMessage(nil), failure.MessageJSON...)
	}
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(failure.Message)))
}
func (failure *Failure) MarshalJSON() ([]byte, error) {
	return protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(failure.Name)))}, protocol.JSONField{Name: "message", Value: failure.MessageValue()}).MarshalJSON()
}
func (failure *Failure) Error() string { return failure.Message }
func typeError(message string) error   { return &Failure{Name: "TypeError", Message: message} }
func property(item *value, name, expression string) (*value, error) {
	if item == nil || null(item) {
		kind := "undefined"
		if null(item) {
			kind = "null"
		}
		return nil, typeError(kind + " is not an object (evaluating '" + expression + "')")
	}
	return item.get(name), nil
}
func messageContent(message *Message, expression string) (*value, error) {
	return property(message.value.get("message"), "content", expression)
}
func blockKind(block *value, expression string) (string, error) {
	field, err := property(block, "type", expression)
	return text(field), err
}
