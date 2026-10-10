package toolschema

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NativeBuiltinDefinition constructs the existing native schema/prompt recipe.
// It never consumes a rendered request or a Source benchmark output.
func NativeBuiltinDefinition(name string) (Definition, error) { return builtinDefinition(name) }

func writeIssue(code, message string, path []string) json.RawMessage {
	return raw(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote(code)}, protocol.JSONField{Name: "path", Value: array(path)}, protocol.JSONField{Name: "message", Value: quote(message)}))
}
func writeInvalidType(expected string, value json.RawMessage, path []string) json.RawMessage {
	received := "undefined"
	if len(value) > 0 {
		switch value[0] {
		case 'n':
			received = "null"
		case '[':
			received = "array"
		case '{':
			received = "object"
		case 't', 'f':
			received = "boolean"
		case '"':
			received = "string"
		default:
			received = "number"
		}
	}
	return raw(protocol.NewJSONObject(protocol.JSONField{Name: "expected", Value: quote(expected)}, protocol.JSONField{Name: "code", Value: quote("invalid_type")}, protocol.JSONField{Name: "path", Value: array(path)}, protocol.JSONField{Name: "message", Value: quote("Invalid input: expected " + expected + ", received " + received)}))
}
func writeHasOwn(input protocol.JSONObject, name string) bool {
	for _, p := range input.OwnProperties() {
		units, _ := protocol.DecodeJavaScriptJSONString(p.NameJSON)
		expected, _ := protocol.DecodeJavaScriptJSONString(quote(name))
		if reflect.DeepEqual(units, expected) {
			return true
		}
	}
	return false
}
func parseWriteInput(input protocol.JSONObject) (protocol.JSONObject, error) {
	issues := []json.RawMessage{}
	if !input.IsObject() {
		return protocol.JSONObject{}, &ValidationError{Issues: array([]json.RawMessage{writeInvalidType("object", raw(input), []string{})})}
	}
	canonical := input.Clone()
	for _, names := range [][]string{{"file_path", "path"}, {"content", "file_text", "file_content"}} {
		present := []string{}
		for _, name := range names {
			if writeHasOwn(input, name) {
				present = append(present, name)
				if _, err := protocol.DecodeJavaScriptJSONString(input.Get(name)); err != nil {
					issues = append(issues, writeIssue("custom", name+" must be a string", []string{name}))
				}
			}
		}
		if len(present) > 0 {
			first := input.Get(present[0])
			conflict := false
			for _, name := range present[1:] {
				conflict = conflict || !writeStrictEqual(first, input.Get(name))
			}
			if conflict {
				issues = append(issues, writeIssue("custom", "Conflicting values for "+strings.Join(present, ", "), []string{names[0]}))
			}
			canonical.Set(names[0], first)
		}
		for _, alias := range names[1:] {
			canonical.Delete(alias)
		}
	}
	if writeHasOwn(input, "description") {
		if _, err := protocol.DecodeJavaScriptJSONString(input.Get("description")); err != nil {
			issues = append(issues, writeIssue("custom", "description must be a string", []string{"description"}))
		}
		canonical.Delete("description")
	}
	if len(issues) > 0 {
		return protocol.JSONObject{}, &ValidationError{Issues: array(issues)}
	}
	result := protocol.NewJSONObject()
	for _, name := range []string{"file_path", "content"} {
		value := canonical.Get(name)
		if _, err := protocol.DecodeJavaScriptJSONString(value); err != nil {
			issues = append(issues, writeInvalidType("string", value, []string{name}))
		} else {
			result.Set(name, value)
		}
	}
	unknown := []json.RawMessage{}
	for _, property := range canonical.OwnProperties() {
		name, _ := protocol.DecodeJavaScriptJSONString(property.NameJSON)
		if !reflect.DeepEqual(name, []uint16{'f', 'i', 'l', 'e', '_', 'p', 'a', 't', 'h'}) && !reflect.DeepEqual(name, []uint16{'c', 'o', 'n', 't', 'e', 'n', 't'}) {
			unknown = append(unknown, protocol.QuoteJavaScriptUTF16(name))
		}
	}
	if len(unknown) > 0 {
		word := "key"
		if len(unknown) > 1 {
			word = "keys"
		}
		message, _ := protocol.DecodeJavaScriptJSONString(quote("Unrecognized " + word + ": "))
		for i, key := range unknown {
			if i > 0 {
				message = append(message, ',', ' ')
			}
			units, _ := protocol.DecodeJavaScriptJSONString(key)
			message = append(message, '"')
			message = append(message, units...)
			message = append(message, '"')
		}
		issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote("unrecognized_keys")}, protocol.JSONField{Name: "keys", Value: array(unknown)}, protocol.JSONField{Name: "path", Value: array([]string{})}, protocol.JSONField{Name: "message", Value: protocol.QuoteJavaScriptUTF16(message)})))
	}
	if len(issues) > 0 {
		return protocol.JSONObject{}, &ValidationError{Issues: array(issues)}
	}
	return result, nil
}

// Preprocess uses JavaScript strict equality on separately parsed JSON values.
// Non-string aliases still receive their custom type issues in declaration order.
func writeStrictEqual(a, b json.RawMessage) bool {
	a = bytes.TrimSpace(a)
	b = bytes.TrimSpace(b)
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	if a[0] == '"' || b[0] == '"' {
		x, e := protocol.DecodeJavaScriptJSONString(a)
		y, f := protocol.DecodeJavaScriptJSONString(b)
		return e == nil && f == nil && reflect.DeepEqual(x, y)
	}
	if a[0] == '{' || a[0] == '[' || b[0] == '{' || b[0] == '[' {
		return false
	}
	if a[0] == 'n' || a[0] == 't' || a[0] == 'f' || b[0] == 'n' || b[0] == 't' || b[0] == 'f' {
		return bytes.Equal(a, b)
	}
	x, e := strconv.ParseFloat(string(a), 64)
	y, f := strconv.ParseFloat(string(b), 64)
	if e != nil && !errorsIsRange(e) {
		return false
	}
	if f != nil && !errorsIsRange(f) {
		return false
	}
	return x == y
}
func errorsIsRange(e error) bool {
	n, ok := e.(*strconv.NumError)
	return ok && n.Err == strconv.ErrRange
}
