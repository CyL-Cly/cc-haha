// Package clistartupenv ports the finite pre-trust settings/env and extra-CA
// exports. It does not run CLI init, configure transports, or alter OS env.
package clistartupenv

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string { return "CLI startup environment capability: " + e.Boundary }

// Environment retains Bun's JS property view. Raw numeric values are not
// stringified prematurely; nil is own undefined, different from absent.
// Mutable arrays/objects and getters are explicitly outside this scalar seam.
// A CLI host retains one instance and serializes its access, like process.env.
type Environment struct{ object protocol.JSONObject }

func NewEnvironment(initial protocol.JSONObject) (*Environment, error) {
	if !initial.IsObject() {
		return nil, &CapabilityError{"original environment object"}
	}
	e := &Environment{object: protocol.NewJSONObject()}
	if err := e.Assign(initial); err != nil {
		return nil, err
	}
	return e, nil
}
func (e *Environment) Snapshot() protocol.JSONObject   { return e.object.Clone() }
func (e *Environment) Get(name string) json.RawMessage { return e.object.Get(name) }
func (e *Environment) SetProperty(key, value json.RawMessage) error {
	if value != nil && (!json.Valid(value) || bytes.ContainsAny(bytes.TrimSpace(value)[:1], "{[")) {
		return &CapabilityError{"mutable/reference or invalid environment value"}
	}
	return e.object.SetPropertyJSON(key, value)
}

// Assign preserves earlier assignments if a later admitted-domain guard fails.
func (e *Environment) Assign(source protocol.JSONObject) error {
	for _, property := range source.OwnProperties() {
		if err := e.SetProperty(property.NameJSON, property.Value); err != nil {
			return err
		}
	}
	return nil
}
func rawKind(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "undefined"
	}
	switch raw[0] {
	case '"':
		return "string"
	case 'n':
		return "null"
	case 't', 'f':
		return "boolean"
	case '{':
		return "object"
	case '[':
		return "array"
	default:
		return "number"
	}
}
func number(raw json.RawMessage) float64 {
	value, _ := strconv.ParseFloat(string(raw), 64)
	return value
}
func truthy(raw json.RawMessage) bool {
	switch rawKind(raw) {
	case "undefined", "null":
		return false
	case "boolean":
		return bytes.Equal(bytes.TrimSpace(raw), []byte("true"))
	case "string":
		units, _ := protocol.DecodeJavaScriptJSONString(raw)
		return len(units) != 0
	case "number":
		value := number(raw)
		return value != 0 && !math.IsNaN(value)
	default:
		return true
	}
}
func stringValue(raw json.RawMessage) string {
	units, _ := protocol.DecodeJavaScriptJSONString(raw)
	return string(utf16.Decode(units))
}
func quote(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func equalString(raw json.RawMessage, value string) bool {
	if rawKind(raw) != "string" {
		return false
	}
	units, _ := protocol.DecodeJavaScriptJSONString(raw)
	other, _ := protocol.DecodeJavaScriptJSONString(quote(value))
	if len(units) != len(other) {
		return false
	}
	for index := range units {
		if units[index] != other[index] {
			return false
		}
	}
	return true
}
func jsWhitespace(r rune) bool {
	return r >= '\t' && r <= '\r' || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}
func envTruthy(raw json.RawMessage) (bool, error) {
	if !truthy(raw) {
		return false, nil
	}
	if rawKind(raw) == "boolean" {
		return true, nil
	}
	if rawKind(raw) != "string" {
		return false, &CapabilityError{"isEnvTruthy non-string/non-boolean input"}
	}
	value := strings.TrimFunc(strings.ToLower(stringValue(raw)), jsWhitespace)
	return value == "1" || value == "true" || value == "yes" || value == "on", nil
}
func concat(parts ...json.RawMessage) json.RawMessage {
	units := []uint16{}
	for _, part := range parts {
		decoded, _ := protocol.DecodeJavaScriptJSONString(part)
		units = append(units, decoded...)
	}
	return protocol.QuoteJavaScriptUTF16(units)
}
func describe(raw json.RawMessage) map[string]any {
	kind := rawKind(raw)
	value := map[string]any{"kind": kind}
	if kind == "undefined" {
		return value
	}
	value["json"] = string(protocol.StringifyValidatedJavaScriptJSON(raw))
	if kind == "number" {
		v := number(raw)
		text, _ := protocol.ExactJavaScriptStringJSON(raw)
		value["nativeString"] = stringValue(text)
		value["finite"] = !math.IsInf(v, 0) && !math.IsNaN(v)
		value["negativeZero"] = v == 0 && math.Signbit(v)
	}
	return value
}
