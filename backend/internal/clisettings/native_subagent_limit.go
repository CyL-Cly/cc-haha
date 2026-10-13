package clisettings

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"strconv"
)

func nativeSubagentLimitErrors(value json.RawMessage, file string) []json.RawMessage {
	if value == nil || string(value) == "null" {
		return nil
	}
	if receivedType(value) != "number" {
		return []json.RawMessage{validationError(file, "maxConcurrentSubagents", "number", receivedType(value))}
	}
	n, e := strconv.ParseFloat(string(value), 64)
	if e != nil || math.IsInf(n, 0) || math.IsNaN(n) {
		return []json.RawMessage{validationError(file, "maxConcurrentSubagents", "number", "number")}
	}
	var errors []json.RawMessage
	if math.Trunc(n) != n {
		errors = append(errors, validationError(file, "maxConcurrentSubagents", "int", "number"))
	}
	issue := func(message string, expected *string) json.RawMessage {
		out := protocol.NewJSONObject(protocol.JSONField{Name: "file", Value: quoted(file)}, protocol.JSONField{Name: "path", Value: quoted("maxConcurrentSubagents")}, protocol.JSONField{Name: "message", Value: quoted(message)})
		if expected != nil {
			out.Set("expected", quoted(*expected))
		}
		raw, _ := out.MarshalJSON()
		return raw
	}
	if n <= 0 {
		bound := "0"
		errors = append(errors, issue("Number must be greater than or equal to 0", &bound))
	}
	if n > 9007199254740991 {
		if math.Trunc(n) == n {
			errors = append(errors, issue("Too big: expected int to be <=9007199254740991", nil))
		}
		errors = append(errors, issue("Too big: expected number to be <=9007199254740991", nil))
	}
	if n < -9007199254740991 && math.Trunc(n) == n {
		bound := "-9007199254740991"
		errors = append([]json.RawMessage{issue("Number must be greater than or equal to -9007199254740991", &bound)}, errors...)
	}
	return errors
}

func IsNativeSubagentLimit(value json.RawMessage) bool {
	return len(nativeSubagentLimitErrors(value, "")) == 0
}
