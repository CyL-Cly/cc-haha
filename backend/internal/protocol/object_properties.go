package protocol

import "encoding/json"

// JSONProperty is one own data property of an ordered JSON record. NameJSON
// preserves UTF-16 code units; nil Value is an existing undefined property.
type JSONProperty struct {
	NameJSON json.RawMessage
	Value    json.RawMessage
}

// OwnProperties returns independent raw snapshots in JavaScript own-key order,
// including undefined slots. It does not invoke accessors or model prototypes.
func (object JSONObject) OwnProperties() []JSONProperty {
	keys := object.propertyKeys()
	properties := make([]JSONProperty, 0, len(keys))
	for _, key := range keys {
		properties = append(properties, JSONProperty{
			NameJSON: append(json.RawMessage(nil), key...),
			Value:    object.state.fields[key].snapshot(),
		})
	}
	return properties
}

// SetPropertyJSON preserves the insertion slot for an existing UTF-16 key.
// The value stays raw until the caller's stringify/validation boundary.
func (object *JSONObject) SetPropertyJSON(name json.RawMessage, value json.RawMessage) error {
	units, err := DecodeJavaScriptJSONString(name)
	if err != nil {
		return err
	}
	object.setQuoted(quoteUTF16(units), value)
	return nil
}

// DeletePropertyJSON deletes only the exact decoded UTF-16 property identity.
func (object *JSONObject) DeletePropertyJSON(name json.RawMessage) error {
	units, err := DecodeJavaScriptJSONString(name)
	if err != nil {
		return err
	}
	if object.state == nil {
		return nil
	}
	key := quoteUTF16(units)
	if _, exists := object.state.fields[key]; exists {
		object.state.version++
	}
	delete(object.state.fields, key)
	for index, existing := range object.state.order {
		if existing == key {
			object.state.order = append(object.state.order[:index:index], object.state.order[index+1:]...)
			break
		}
	}
	return nil
}
