package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"unicode/utf16"
)

type objectState struct {
	order   []string
	fields  map[string]*jsonValue
	version uint64
}

// JSONObject is an ordered record of raw JSON fields, used for model-visible
// content. Updating a field preserves its insertion slot; deleting and adding
// it places it at the end. It retains UTF-16 property identity and numeric
// lexemes until the caller's final JavaScript stringify boundary.
//
// Initialized value copies retain the same top-level object identity. Clone
// makes an independent outer record while nested object/array values stay shared.
// Access must still be coordinated by the owning caller during mutation.
type JSONObject struct {
	state *objectState
}

type JSONField struct {
	Name  string
	Value json.RawMessage
}

func NewJSONObject(fields ...JSONField) JSONObject {
	object := JSONObject{state: &objectState{fields: map[string]*jsonValue{}}}
	for _, field := range fields {
		object.Set(field.Name, field.Value)
	}
	return object
}

func objectName(name string) string { return quoteUTF16(utf16.Encode([]rune(name))) }

func (object JSONObject) IsObject() bool { return object.state != nil }

func (object JSONObject) Get(name string) json.RawMessage {
	if object.state == nil {
		return nil
	}
	return object.state.fields[objectName(name)].snapshot()
}

func (object *JSONObject) setReference(name string, value *jsonValue) {
	if object.state == nil {
		object.state = &objectState{fields: map[string]*jsonValue{}}
	}
	if _, exists := object.state.fields[name]; !exists {
		object.state.order = append(object.state.order, name)
	}
	object.state.fields[name] = value
	object.state.version++
}
func (object *JSONObject) setQuoted(name string, value json.RawMessage) {
	object.setReference(name, rawJSONValue(value))
}

// A nil field represents undefined and is omitted, rather than serialized as
// null. Explicit null is the raw JSON token "null".
func (object *JSONObject) Set(name string, value json.RawMessage) {
	object.setQuoted(objectName(name), value)
}

func (object *JSONObject) Delete(name string) {
	if object.state == nil {
		return
	}
	quoted := objectName(name)
	if _, exists := object.state.fields[quoted]; exists {
		object.state.version++
	}
	delete(object.state.fields, quoted)
	for index, key := range object.state.order {
		if key == quoted {
			object.state.order = append(object.state.order[:index:index], object.state.order[index+1:]...)
			break
		}
	}
}

func (object JSONObject) Clone() JSONObject {
	if !object.IsObject() {
		return JSONObject{}
	}
	copy := JSONObject{state: &objectState{order: append([]string(nil), object.state.order...), fields: map[string]*jsonValue{}}}
	for key, value := range object.state.fields {
		copy.state.fields[key] = value
	}
	return copy
}

// Assign follows Object.assign's own property enumeration, including fields
// whose value is undefined. It retains quoted UTF-16 keys and existing target
// insertion slots. Nested values are shared; scalar/raw buffers are private and immutable.
func (object *JSONObject) Assign(source JSONObject) {
	for _, key := range source.propertyKeys() {
		object.setReference(key, source.state.fields[key])
	}
}

func (object JSONObject) propertyKeys() []string {
	if object.state == nil {
		return nil
	}
	keys := append([]string(nil), object.state.order...)
	sort.SliceStable(keys, func(left, right int) bool {
		a, aIndex := jsArrayIndex(keys[left])
		b, bIndex := jsArrayIndex(keys[right])
		if aIndex && bIndex {
			return a < b
		}
		return aIndex && !bIndex
	})
	return keys
}

func (object *JSONObject) UnmarshalJSON(raw []byte) error {
	if object == nil {
		return errors.New("cannot decode JSON object into a nil receiver")
	}
	if !json.Valid(raw) {
		return errors.New("ordered JSON object requires valid JSON")
	}
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		*object = JSONObject{}
		return nil
	}
	if raw[0] != '{' {
		return errors.New("ordered JSON object requires an object or null")
	}
	parsed := NewJSONObject()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	_, _ = decoder.Token()
	for decoder.More() {
		start := decoder.InputOffset()
		_, _ = decoder.Token()
		key := raw[start:decoder.InputOffset()]
		key = key[bytes.IndexByte(key, '"'):]
		var value json.RawMessage
		_ = decoder.Decode(&value) // The complete input was validated above.
		parsed.setQuoted(quoteUTF16(decodeJSString(key)), value)
	}
	*object = parsed
	return nil
}

func (object JSONObject) MarshalJSON() ([]byte, error) {
	return renderObject(object, map[any]bool{}, true)
}
