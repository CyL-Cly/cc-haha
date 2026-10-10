package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// jsonValue is the actual field value. Raw snapshots remain private; an object
// or array access resolves the existing cell once, on the owning caller. A
// shallow spread shares this cell before and after nested access. Serialization
// reads the same references, retaining untouched original JSON lexemes.
type jsonValue struct {
	raw                         json.RawMessage
	object                      *objectState
	array                       *arrayState
	objectVersion, arrayVersion uint64
}
type arrayState struct {
	values  []*jsonValue
	version uint64
}
type JSONArray struct{ state *arrayState }

func rawJSONValue(raw json.RawMessage) *jsonValue {
	return &jsonValue{raw: append(json.RawMessage(nil), raw...)}
}
func (value *jsonValue) resolveObject() (JSONObject, bool) {
	if value == nil {
		return JSONObject{}, false
	}
	if value.object != nil {
		return JSONObject{state: value.object}, true
	}
	data := bytes.TrimSpace(value.raw)
	if len(data) == 0 || data[0] != '{' || !json.Valid(data) {
		return JSONObject{}, false
	}
	var parsed JSONObject
	if err := parsed.UnmarshalJSON(data); err != nil {
		return JSONObject{}, false
	}
	value.object = parsed.state
	value.objectVersion = parsed.state.version
	return parsed, true
}
func (value *jsonValue) resolveArray() (JSONArray, bool) {
	if value == nil {
		return JSONArray{}, false
	}
	if value.array != nil {
		return JSONArray{state: value.array}, true
	}
	data := bytes.TrimSpace(value.raw)
	if len(data) == 0 || data[0] != '[' || !json.Valid(data) {
		return JSONArray{}, false
	}
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return JSONArray{}, false
	}
	state := &arrayState{values: make([]*jsonValue, 0, len(items))}
	for _, item := range items {
		state.values = append(state.values, rawJSONValue(item))
	}
	value.array = state
	value.arrayVersion = state.version
	return JSONArray{state: state}, true
}
func (value *jsonValue) pristine() bool {
	if value == nil {
		return true
	}
	if value.object != nil {
		if value.raw == nil || value.object.version != value.objectVersion {
			return false
		}
		for _, child := range value.object.fields {
			if !child.pristine() {
				return false
			}
		}
	}
	if value.array != nil {
		if value.raw == nil || value.array.version != value.arrayVersion {
			return false
		}
		for _, child := range value.array.values {
			if !child.pristine() {
				return false
			}
		}
	}
	return true
}
func (value *jsonValue) render(path map[any]bool, validate bool) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	if value.pristine() {
		if validate && value.raw != nil && !json.Valid(value.raw) {
			return nil, errors.New("ordered JSON value contains invalid JSON")
		}
		return append([]byte(nil), value.raw...), nil
	}
	if value.object != nil {
		return renderObject(JSONObject{state: value.object}, path, validate)
	}
	if value.array != nil {
		return renderArray(JSONArray{state: value.array}, path, validate)
	}
	return append([]byte(nil), value.raw...), nil
}
func renderObject(object JSONObject, path map[any]bool, validate bool) ([]byte, error) {
	if !object.IsObject() {
		return []byte("null"), nil
	}
	if path[object.state] {
		return nil, errors.New("ordered JSON object contains circular reference")
	}
	path[object.state] = true
	defer delete(path, object.state)
	output := []byte{'{'}
	for _, key := range object.propertyKeys() {
		value, err := object.state.fields[key].render(path, validate)
		if err != nil {
			return nil, fmt.Errorf("ordered JSON object field %s: %w", key, err)
		}
		if value == nil {
			continue
		}
		if len(output) > 1 {
			output = append(output, ',')
		}
		output = append(output, key...)
		output = append(output, ':')
		output = append(output, value...)
	}
	return append(output, '}'), nil
}
func renderArray(array JSONArray, path map[any]bool, validate bool) ([]byte, error) {
	if array.state == nil {
		return []byte("null"), nil
	}
	if path[array.state] {
		return nil, errors.New("ordered JSON array contains circular reference")
	}
	path[array.state] = true
	defer delete(path, array.state)
	output := []byte{'['}
	for i, item := range array.state.values {
		value, err := item.render(path, validate)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			output = append(output, ',')
		}
		if value == nil {
			value = []byte("null")
		}
		output = append(output, value...)
	}
	return append(output, ']'), nil
}
func (value *jsonValue) snapshot() json.RawMessage {
	raw, _ := value.render(map[any]bool{}, false)
	return append(json.RawMessage(nil), raw...)
}

// Object/Array return actual nested references. Like Set, these methods belong
// on the existing event owner. Get/OwnProperties remain independent byte reads.
func (object JSONObject) Object(name string) (JSONObject, bool) {
	if object.state == nil {
		return JSONObject{}, false
	}
	return object.state.fields[objectName(name)].resolveObject()
}
func (object JSONObject) Array(name string) (JSONArray, bool) {
	if object.state == nil {
		return JSONArray{}, false
	}
	return object.state.fields[objectName(name)].resolveArray()
}
func (object *JSONObject) SetObject(name string, value JSONObject) {
	if !value.IsObject() {
		object.Set(name, json.RawMessage("null"))
		return
	}
	object.setReference(objectName(name), &jsonValue{object: value.state})
}
func (object *JSONObject) SetArray(name string, value JSONArray) {
	if value.state == nil {
		object.Set(name, json.RawMessage("null"))
		return
	}
	object.setReference(objectName(name), &jsonValue{array: value.state})
}
func (array JSONArray) Len() int {
	if array.state == nil {
		return 0
	}
	return len(array.state.values)
}
func (array JSONArray) Get(index int) json.RawMessage {
	if index < 0 || index >= array.Len() {
		return nil
	}
	return array.state.values[index].snapshot()
}
func (array JSONArray) Object(index int) (JSONObject, bool) {
	if index < 0 || index >= array.Len() {
		return JSONObject{}, false
	}
	return array.state.values[index].resolveObject()
}
func (array JSONArray) Array(index int) (JSONArray, bool) {
	if index < 0 || index >= array.Len() {
		return JSONArray{}, false
	}
	return array.state.values[index].resolveArray()
}

// Select is Array.filter's new array, retaining each original field value.
// It does not deep-copy a kept object or remove an item from the original array.
func (array JSONArray) Select(indices []int) (JSONArray, error) {
	values := make([]*jsonValue, 0, len(indices))
	for _, i := range indices {
		if i < 0 || i >= array.Len() {
			return JSONArray{}, errors.New("JSON array selection index out of bounds")
		}
		values = append(values, array.state.values[i])
	}
	return JSONArray{state: &arrayState{values: values}}, nil
}
func (array JSONArray) Clone() JSONArray {
	if array.state == nil {
		return JSONArray{}
	}
	return JSONArray{state: &arrayState{values: append([]*jsonValue(nil), array.state.values...)}}
}
func (array JSONArray) Set(index int, value json.RawMessage) error {
	if index < 0 || index >= array.Len() {
		return errors.New("JSON array assignment index out of bounds")
	}
	array.state.values[index] = rawJSONValue(value)
	array.state.version++
	return nil
}
func (array JSONArray) Append(value json.RawMessage) error {
	if array.state == nil {
		return errors.New("JSON array append requires initialized array")
	}
	array.state.values = append(array.state.values, rawJSONValue(value))
	array.state.version++
	return nil
}
func (array JSONArray) MarshalJSON() ([]byte, error) { return renderArray(array, map[any]bool{}, true) }

func (object JSONObject) ObjectPropertyJSON(name json.RawMessage) (JSONObject, bool, error) {
	units, err := DecodeJavaScriptJSONString(name)
	if err != nil {
		return JSONObject{}, false, err
	}
	if object.state == nil {
		return JSONObject{}, false, nil
	}
	value, ok := object.state.fields[quoteUTF16(units)].resolveObject()
	return value, ok, nil
}
func (object JSONObject) ArrayPropertyJSON(name json.RawMessage) (JSONArray, bool, error) {
	units, err := DecodeJavaScriptJSONString(name)
	if err != nil {
		return JSONArray{}, false, err
	}
	if object.state == nil {
		return JSONArray{}, false, nil
	}
	value, ok := object.state.fields[quoteUTF16(units)].resolveArray()
	return value, ok, nil
}
