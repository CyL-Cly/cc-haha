package clisettings

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This is the JSON/own-undefined merge seam, not a generic lodash runtime.
// Prototype-named graph mutation and array-target/object-source merges need
// their own Source proofs and stop before any mark or physical write.
func checkMergeValues(object protocol.JSONObject) error {
	for _, property := range object.OwnProperties() {
		if bytes.Equal(property.NameJSON, []byte(`"__proto__"`)) || bytes.Equal(property.NameJSON, []byte(`"constructor"`)) || bytes.Equal(property.NameJSON, []byte(`"prototype"`)) {
			return &CapabilityError{"prototype-named merge property"}
		}
		raw := bytes.TrimSpace(property.Value)
		if len(raw) == 0 {
			continue
		}
		if !json.Valid(raw) {
			return &CapabilityError{"invalid raw JSON property"}
		}
		if raw[0] == '{' {
			var nested protocol.JSONObject
			_ = nested.UnmarshalJSON(raw)
			if err := checkMergeValues(nested); err != nil {
				return err
			}
		}
		if raw[0] == '[' {
			var elements []json.RawMessage
			_ = json.Unmarshal(raw, &elements)
			for _, element := range elements {
				wrapper := protocol.NewJSONObject(protocol.JSONField{Name: "value", Value: element})
				if err := checkMergeValues(wrapper); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func mergeObjects(target *protocol.JSONObject, source protocol.JSONObject) error {
	for _, property := range source.OwnProperties() {
		if property.Value == nil {
			if err := target.DeletePropertyJSON(property.NameJSON); err != nil {
				return err
			}
			_ = target.SetPropertyJSON(property.NameJSON, nil)
			continue
		}
		raw := bytes.TrimSpace(property.Value)
		if raw[0] == '{' {
			previous := propertyValue(*target, property.NameJSON)
			if receivedType(previous) == "array" {
				return &CapabilityError{"object merge into existing array"}
			}
			nested := protocol.NewJSONObject()
			if receivedType(previous) == "object" {
				_ = nested.UnmarshalJSON(previous)
			}
			var incoming protocol.JSONObject
			_ = incoming.UnmarshalJSON(raw)
			if err := mergeObjects(&nested, incoming); err != nil {
				return err
			}
			raw, _ = nested.MarshalJSON()
		}
		if err := target.SetPropertyJSON(property.NameJSON, raw); err != nil {
			return err
		}
	}
	return nil
}
func propertyValue(object protocol.JSONObject, key json.RawMessage) json.RawMessage {
	units, err := protocol.DecodeJavaScriptJSONString(key)
	if err != nil {
		return nil
	}
	key = protocol.QuoteJavaScriptUTF16(units)
	for _, property := range object.OwnProperties() {
		if bytes.Equal(property.NameJSON, key) {
			return property.Value
		}
	}
	return nil
}
func setUndefinedPath(object *protocol.JSONObject, parts []json.RawMessage) error {
	if len(parts) == 0 {
		return &CapabilityError{"empty undefined path"}
	}
	if _, err := protocol.DecodeJavaScriptJSONString(parts[0]); err != nil {
		return &CapabilityError{"nonstring undefined path"}
	}
	if len(parts) == 1 {
		_ = object.DeletePropertyJSON(parts[0])
		return object.SetPropertyJSON(parts[0], nil)
	}
	raw := propertyValue(*object, parts[0])
	if receivedType(raw) != "object" {
		return &CapabilityError{"undefined path through nonobject"}
	}
	var nested protocol.JSONObject
	_ = nested.UnmarshalJSON(raw)
	if err := setUndefinedPath(&nested, parts[1:]); err != nil {
		return err
	}
	// Raw JSON cannot represent nested undefined. Apply those deletions here;
	// their presence is carried separately to the merge by the patch path tags.
	value, _ := nested.MarshalJSON()
	return object.SetPropertyJSON(parts[0], value)
}

func validateUndefinedPath(object protocol.JSONObject, parts []json.RawMessage) error {
	if len(parts) == 0 {
		return &CapabilityError{"empty undefined path"}
	}
	if _, err := protocol.DecodeJavaScriptJSONString(parts[0]); err != nil {
		return &CapabilityError{"nonstring undefined path"}
	}
	if len(parts) == 1 {
		return nil
	}
	raw := propertyValue(object, parts[0])
	if receivedType(raw) != "object" {
		return &CapabilityError{"undefined path through nonobject"}
	}
	var nested protocol.JSONObject
	_ = nested.UnmarshalJSON(raw)
	return validateUndefinedPath(nested, parts[1:])
}
