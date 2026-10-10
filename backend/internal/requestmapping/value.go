// Package requestmapping ports the default-release Anthropic message mapping
// seam. It does not select providers, TTL eligibility, or cache-editing policy.
package requestmapping

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// Value is local to this request mapping seam. Array/object nodes retain the
// shallow references of the source functions; primitives retain their original
// JSON, including UTF-16 strings and numeric lexemes. It is not a JS runtime or
// a replacement for the shared RawMessage history/persistence representation.
type Value struct {
	kind       byte
	raw        json.RawMessage
	properties map[string]*Value
	order      []string
	elements   []*Value
}

func ParseValue(raw json.RawMessage) (*Value, error) {
	if !json.Valid(raw) {
		return nil, errors.New("request mapping value requires valid JSON")
	}
	return parseValue(bytes.TrimSpace(raw)), nil
}

// The one public validation precedes all structured stdlib/storage decoding.
func parseValue(raw json.RawMessage) *Value {
	value := &Value{kind: raw[0]}
	switch value.kind {
	case '{':
		decoder := json.NewDecoder(bytes.NewReader(raw))
		_, _ = decoder.Token()
		value.properties = map[string]*Value{}
		for decoder.More() {
			start := decoder.InputOffset()
			_, _ = decoder.Token()
			quoted := raw[start:decoder.InputOffset()]
			quoted = quoted[bytes.IndexByte(quoted, '"'):]
			// Let the existing codec derive a UTF-16-safe Go property key.
			singleton := append(append([]byte{'{'}, quoted...), []byte(":null}")...)
			var field storage.Object
			_ = json.Unmarshal(singleton, &field)
			var child json.RawMessage
			_ = decoder.Decode(&child)
			for key := range field {
				value.SetProperty(key, parseValue(bytes.TrimSpace(child)))
			}
		}
	case '[':
		var entries []json.RawMessage
		_ = json.Unmarshal(raw, &entries)
		value.elements = make([]*Value, 0, len(entries))
		for _, child := range entries {
			value.elements = append(value.elements, parseValue(bytes.TrimSpace(child)))
		}
	default:
		value.raw = append(json.RawMessage(nil), raw...)
	}
	return value
}

func (value *Value) MarshalJSON() ([]byte, error) {
	if value == nil {
		return []byte("null"), nil
	}
	switch value.kind {
	case '{':
		output := []byte{'{'}
		for _, key := range value.order {
			child := value.properties[key]
			if child == nil {
				continue
			} // undefined object properties are omitted.
			if len(output) > 1 {
				output = append(output, ',')
			}
			quoted, _ := json.Marshal(storage.Object{key: json.RawMessage("0")})
			output = append(output, quoted[1:bytes.LastIndexByte(quoted, ':')]...)
			output = append(output, ':')
			raw, _ := child.MarshalJSON()
			output = append(output, raw...)
		}
		return protocol.ParseAndStringifyJavaScriptJSON(append(output, '}'))
	case '[':
		raw, _ := json.Marshal(value.elements)
		return protocol.ParseAndStringifyJavaScriptJSON(raw)
	default:
		return append([]byte(nil), value.raw...), nil
	}
}

// Property/Element are the narrow accessors required by envelope mapping and
// its alias regression tests. Missing/undefined values are represented by nil.
func (value *Value) Property(key string) *Value {
	if value == nil {
		return nil
	}
	return value.properties[key]
}

func (value *Value) Element(index int) *Value {
	if value == nil || index < 0 || index >= len(value.elements) {
		return nil
	}
	return value.elements[index]
}

func (value *Value) ArrayLength() (int, bool) {
	if value == nil || value.kind != '[' {
		return 0, false
	}
	return len(value.elements), true
}

// SetProperty, SetElement and Append model only the caller mutations needed
// to prove shallow cloning. They do not implement coercion or JS prototypes.
func (value *Value) SetProperty(key string, child *Value) bool {
	if value == nil || value.kind != '{' {
		return false
	}
	if _, exists := value.properties[key]; !exists {
		value.order = append(value.order, key)
	}
	value.properties[key] = child
	return true
}

func (value *Value) SetElement(index int, child *Value) bool {
	if value == nil || value.kind != '[' || index < 0 || index >= len(value.elements) {
		return false
	}
	value.elements[index] = child
	return true
}

func (value *Value) Append(child *Value) bool {
	if value == nil || value.kind != '[' {
		return false
	}
	value.elements = append(value.elements, child)
	return true
}

func objectValue(fields map[string]*Value, order ...string) *Value {
	return &Value{kind: '{', properties: fields, order: order}
}
func arrayValue(elements []*Value) *Value { return &Value{kind: '[', elements: elements} }
func literalString(value string) *Value {
	raw, _ := json.Marshal(value)
	return &Value{kind: '"', raw: raw}
}
func nullish(value *Value) bool { return value == nil || value.kind == 'n' }

func stringEquals(value *Value, expected string) bool {
	if value == nil || value.kind != '"' {
		return false
	}
	var decoded string
	_ = json.Unmarshal(value.raw, &decoded)
	return decoded == expected
}

func spread(value *Value) *Value {
	result := objectValue(map[string]*Value{})
	if value != nil {
		switch value.kind {
		case '{':
			for _, key := range value.order {
				result.SetProperty(key, value.properties[key])
			}
		case '[':
			for index, child := range value.elements {
				result.SetProperty(strconv.Itoa(index), child)
			}
		case '"':
			units, _ := protocol.DecodeJavaScriptJSONString(value.raw)
			for index, unit := range units {
				result.SetProperty(strconv.Itoa(index), &Value{kind: '"', raw: protocol.QuoteJavaScriptUTF16([]uint16{unit})})
			}
		}
	}
	return result
}
