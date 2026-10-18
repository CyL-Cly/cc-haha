package protocol

import "encoding/json"

// GetOrdinaryPropertyStringJSON performs String(object[key]) for an ordinary
// JSON object's defined property. Missing/own-undefined returns nil so the
// caller retains its original missing-value policy. It deliberately does not
// change GetPropertyJSON's own-property contract or create callable JSON data.
func (object JSONObject) GetOrdinaryPropertyStringJSON(name json.RawMessage) (json.RawMessage, error) {
	units, err := DecodeJavaScriptJSONString(name)
	if err != nil {
		return nil, err
	}
	if object.state != nil {
		if own := object.state.fields[quoteUTF16(units)]; own != nil {
			value := own.snapshot()
			if value == nil {
				return nil, nil
			}
			return ExactJavaScriptStringJSON(value)
		}
	}
	// JSON input cannot install a custom prototype or callable own method.
	// These are Object.prototype's string-keyed properties in the frozen Bun
	// 1.3.14 reference. Its Object constructor has a distinct native spelling.
	key := make([]byte, len(units))
	for i, unit := range units {
		if unit > 127 {
			return nil, nil
		}
		key[i] = byte(unit)
	}
	switch name := string(key); name {
	case "constructor":
		return json.RawMessage(`"function Object() { [native code] }"`), nil
	case "__proto__":
		return json.RawMessage(`"[object Object]"`), nil
	case "toString", "toLocaleString", "valueOf", "hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable", "__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__":
		return QuoteJavaScriptUTF16(asciiUnits("function " + name + "() {\n    [native code]\n}")), nil
	default:
		return nil, nil
	}
}

func asciiUnits(value string) []uint16 {
	units := make([]uint16, len(value))
	for i := range value {
		units[i] = uint16(value[i])
	}
	return units
}
