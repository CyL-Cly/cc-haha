// Package toolfeedback builds model-visible rejection envelopes. Execution,
// permissions and hooks remain separate callers of this shared message seam.
package toolfeedback

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func marker(input json.RawMessage) ([]uint16, json.RawMessage, bool) {
	object, valid, err := storage.NormalizeObject(input)
	if err != nil || !valid || len(object) != 1 {
		return nil, nil, false
	}
	value, valid, err := storage.NormalizeObject(object["__unparsedToolInput"])
	if err != nil || !valid {
		return nil, nil, false
	}
	units, err := protocol.DecodeJavaScriptJSONString(value["raw"])
	if err != nil {
		return nil, nil, false
	}
	length := bytes.TrimSpace(value["len"])
	if !json.Valid(length) || len(length) == 0 || !(length[0] == '-' || length[0] >= '0' && length[0] <= '9') {
		return nil, nil, false
	}
	return units, length, true
}

func IsUnparsedToolInput(input json.RawMessage) bool {
	_, _, recognized := marker(input)
	return recognized
}

func join(parts ...json.RawMessage) (json.RawMessage, error) {
	return protocol.JoinJavaScriptValuesJSON(parts, "")
}
func text(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }

func envelope(factory *messages.Factory, use storage.Object, assistantUUID, content, result json.RawMessage) (storage.Object, error) {
	if factory == nil {
		return nil, errors.New("tool feedback requires the shared session message factory")
	}
	block := storage.Object{"type": text("tool_result"), "tool_use_id": use["id"], "is_error": json.RawMessage("true"), "content": content}
	blocks, err := json.Marshal([]storage.Object{block})
	if err != nil {
		return nil, err
	}
	return factory.CreateUser(storage.Object{"content": blocks, "toolUseResult": result, "sourceToolAssistantUUID": assistantUUID}), nil
}

// InvalidInput returns handled=false for valid/ordinary inputs. The caller
// must take a handled rejection before resolving schemas, permissions or tools.
func InvalidInput(factory *messages.Factory, use storage.Object, assistantUUID json.RawMessage) (storage.Object, bool, error) {
	units, length, recognized := marker(use["input"])
	if !recognized {
		return nil, false, nil
	}
	preview := units
	if len(preview) > 200 {
		preview = preview[:200]
	}
	content, err := join(text("<tool_use_error>InputValidationError: "), use["name"], text(" was called with input that could not be parsed as JSON.\n"), text(fmt.Sprintf("You sent (first %d of ", len(preview))), length, text(" bytes): "), protocol.QuoteJavaScriptUTF16(preview), text("\nCommon causes: unescaped backslashes in file paths (use / or \\\\), unescaped control characters, or truncated output. Retry with valid JSON.</tool_use_error>"))
	if err != nil {
		return nil, true, err
	}
	result, err := join(text("InputValidationError: JSON parse failed ("), length, text(" bytes)"))
	if err != nil {
		return nil, true, err
	}
	message, err := envelope(factory, use, assistantUUID, content, result)
	return message, true, err
}

func UnknownTool(factory *messages.Factory, use storage.Object, assistantUUID json.RawMessage) (storage.Object, error) {
	result, err := join(text("Error: No such tool available: "), use["name"])
	if err != nil {
		return nil, err
	}
	content, err := join(text("<tool_use_error>"), result, text("</tool_use_error>"))
	if err != nil {
		return nil, err
	}
	return envelope(factory, use, assistantUUID, content, result)
}

// MarkerFromJSONText is the history/import seam for a quoted provider argument
// string. It retains UTF-16 units; it never repairs the invalid JSON to {}.
func MarkerFromJSONText(raw json.RawMessage) (json.RawMessage, error) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return nil, err
	}
	return protocol.CreateUnparsedToolInput(units), nil
}
