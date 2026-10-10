package clistartupenv

import (
	"bytes"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func propertyValue(object protocol.JSONObject, key json.RawMessage) (json.RawMessage, bool) {
	for _, property := range object.OwnProperties() {
		if bytes.Equal(key, property.NameJSON) {
			return property.Value, true
		}
	}
	return nil, false
}
func scalarEqual(left, right json.RawMessage) bool {
	if rawKind(left) != rawKind(right) {
		return false
	}
	switch rawKind(left) {
	case "undefined":
		return true
	case "object", "array":
		return false // independent JSON allocations, not deep equality
	case "number":
		return number(left) == number(right) // SameValueZero: +/-0 deduplicated
	default:
		return bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(left), protocol.StringifyValidatedJavaScriptJSON(right))
	}
}

// The actual settingsMergeCustomizer concatenates arrays and uniq uses JS
// identity. This finite profile starts with independent original JSON files
// and inline objects; reference-alias mutations are not an input adapter.
func mergeSettings(target *protocol.JSONObject, source protocol.JSONObject) error {
	for _, property := range source.OwnProperties() {
		prior, own := propertyValue(*target, property.NameJSON)
		incoming := property.Value
		if incoming == nil && own {
			continue
		} // lodash merge keeps a prior defined value
		if rawKind(incoming) == "object" {
			if rawKind(prior) == "array" {
				return &CapabilityError{"settings object merged into array"}
			}
			nested := protocol.NewJSONObject()
			if rawKind(prior) == "object" {
				_ = nested.UnmarshalJSON(prior)
			}
			var next protocol.JSONObject
			_ = next.UnmarshalJSON(incoming)
			if err := mergeSettings(&nested, next); err != nil {
				return err
			}
			incoming, _ = nested.MarshalJSON()
		} else if rawKind(incoming) == "array" && rawKind(prior) == "array" {
			var left, right []json.RawMessage
			_ = json.Unmarshal(prior, &left)
			_ = json.Unmarshal(incoming, &right)
			all := []json.RawMessage{}
			for _, item := range append(left, right...) {
				// Source uniq spreads a Set. Set insertion canonicalizes -0 to
				// +0; JSON bytes alone cannot reveal this runtime distinction.
				if rawKind(item) == "number" && number(item) == 0 {
					item = []byte("0")
				}
				seen := false
				for _, existing := range all {
					if scalarEqual(item, existing) {
						seen = true
						break
					}
				}
				if !seen {
					all = append(all, item)
				}
			}
			incoming, _ = json.Marshal(all)
		}
		if err := target.SetPropertyJSON(property.NameJSON, incoming); err != nil {
			return err
		}
	}
	return nil
}
