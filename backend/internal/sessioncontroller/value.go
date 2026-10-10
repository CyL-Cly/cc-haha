// Package sessioncontroller owns the finite headless activation/admission host.
// It retains host state around the shared resume and request boundaries.
package sessioncontroller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "headless session capability is not migrated: " + e.Boundary
}
func capability(boundary string) error { return &CapabilityError{boundary} }
func quote(value string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(value)))
}
func raw(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
func object(value json.RawMessage) protocol.JSONObject {
	var result protocol.JSONObject
	_ = json.Unmarshal(value, &result)
	return result
}
func text(value json.RawMessage) string {
	var result string
	_ = json.Unmarshal(value, &result)
	return result
}
func truthy(value json.RawMessage) bool {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) || bytes.Equal(value, []byte("false")) || bytes.Equal(value, []byte("0")) {
		return false
	}
	if value[0] == '"' {
		units, err := protocol.DecodeJavaScriptJSONString(value)
		return err == nil && len(units) > 0
	}
	if number, err := strconv.ParseFloat(string(value), 64); err == nil {
		return number != 0
	}
	return true
}
func envTruthy(value string) bool {
	switch strings.ToLower(utf16Text(trimUnits(utf16.Encode([]rune(value))))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func stringKey(value json.RawMessage) (string, error) {
	units, err := protocol.DecodeJavaScriptJSONString(value)
	if err != nil {
		return "", capability("non-string UUID identity")
	}
	return string(protocol.QuoteJavaScriptUTF16(units)), nil
}
func parseObjects(value json.RawMessage) ([]protocol.JSONObject, error) {
	var result []protocol.JSONObject
	if err := json.Unmarshal(value, &result); err != nil {
		return nil, fmt.Errorf("headless envelope array: %w", err)
	}
	return result, nil
}
