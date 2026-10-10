package clisettings

import (
	"bytes"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// parseStatusLine mirrors the command status-line settings schema. Parsing the
// setting is deliberately separate from executing it: settings reads and
// desktop server startup must never run a user's command.
func parseStatusLine(input protocol.JSONObject, out *protocol.JSONObject, result *ParsedSettings, file string) {
	raw := input.Get("statusLine")
	if raw == nil {
		return
	}
	if receivedType(raw) != "object" {
		result.Errors = append(result.Errors, validationError(file, "statusLine", "object", receivedType(raw)))
		return
	}
	var value protocol.JSONObject
	if err := value.UnmarshalJSON(raw); err != nil {
		result.Errors = append(result.Errors, validationError(file, "statusLine", "object", "invalid"))
		return
	}
	next := protocol.NewJSONObject()

	typeValue := value.Get("type")
	if typeValue == nil || !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(typeValue), []byte(`"command"`)) {
		result.Errors = append(result.Errors, enumError(file, "statusLine.type", []string{"command"}))
	} else {
		next.Set("type", typeValue)
	}

	command := value.Get("command")
	if receivedType(command) != "string" {
		result.Errors = append(result.Errors, validationError(file, "statusLine.command", "string", receivedType(command)))
	} else {
		next.Set("command", command)
	}

	if padding := value.Get("padding"); padding != nil {
		if receivedType(padding) != "number" {
			result.Errors = append(result.Errors, validationError(file, "statusLine.padding", "number", receivedType(padding)))
		} else {
			next.Set("padding", padding)
		}
	}
	encoded, _ := next.MarshalJSON()
	out.Set("statusLine", encoded)
}

func validateStatusLinePatch(input protocol.JSONObject) error {
	raw := input.Get("statusLine")
	if raw == nil {
		return nil
	}
	result := &ParsedSettings{Errors: []json.RawMessage{}}
	parseStatusLine(protocol.NewJSONObject(protocol.JSONField{Name: "statusLine", Value: raw}), &protocol.JSONObject{}, result, "settings")
	if len(result.Errors) == 0 {
		return nil
	}
	return &CapabilityError{"invalid statusLine settings"}
}
