package sidequestion

import (
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/apipresentation"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func Extract(records []protocol.JSONObject) (json.RawMessage, error) {
	return ExtractWithAPIError(records, nil)
}
func ExtractWithAPIError(records []protocol.JSONObject, apiErrorMessage json.RawMessage) (json.RawMessage, error) {
	texts := []json.RawMessage{}
	for _, record := range records {
		if record.String("type") != "assistant" {
			continue
		}
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(object(record.Get("message")).Get("content"), &blocks)
		if string(record.Get("isApiErrorMessage")) == "true" {
			parts := []json.RawMessage{}
			for _, block := range blocks {
				if block.String("type") == "text" {
					parts = append(parts, block.Get("text"))
				}
			}
			value, e := protocol.JoinJavaScriptValuesJSON(parts, "\n")
			if e != nil {
				return nil, e
			}
			value, e = trimJSON(value)
			if e != nil {
				return nil, e
			}
			units, _ := protocol.DecodeJavaScriptJSONString(value)
			if len(units) == 0 {
				return nil, errors.New("Side question failed")
			}
			return nil, &MessageError{value}
		}
		for _, block := range blocks {
			if block.String("type") == "text" {
				texts = append(texts, block.Get("text"))
			}
		}
	}
	value, e := protocol.JoinJavaScriptValuesJSON(texts, "\n\n")
	if e != nil {
		return nil, e
	}
	value, e = trimJSON(value)
	if e != nil {
		return nil, e
	}
	units, _ := protocol.DecodeJavaScriptJSONString(value)
	if len(units) == 0 {
		for _, record := range records {
			if record.String("type") == "system" && record.String("subtype") == "api_error" {
				if apiErrorMessage != nil {
					return nil, &MessageError{apiErrorMessage}
				}
				value, e := apipresentation.FormatAPIErrorRecord(object(record.Get("error")))
				if e != nil {
					return nil, e
				}
				return nil, &MessageError{value}
			}
		}
		return nil, errors.New("No response received")
	}
	return value, nil
}

// MessageError retains the actual JS Error.message, including lone UTF16.
type MessageError struct{ MessageJSON json.RawMessage }

func (e *MessageError) Error() string                     { return propertyName(e.MessageJSON) }
func (e *MessageError) ErrorMessageJSON() json.RawMessage { return e.MessageJSON }
func ErrorMessageJSON(e error) json.RawMessage {
	if raw, ok := e.(interface{ ErrorMessageJSON() json.RawMessage }); ok {
		return append(json.RawMessage(nil), raw.ErrorMessageJSON()...)
	}
	return quote(e.Error())
}

// CheckMessage is Source runSideQuestion's onMessage cutpoint, before any
// PreToolUse hook, permission check or dispatch. Names retain raw JS units.
func CheckMessage(record protocol.JSONObject) error {
	if record.String("type") != "assistant" {
		return nil
	}
	var blocks []protocol.JSONObject
	_ = json.Unmarshal(object(record.Get("message")).Get("content"), &blocks)
	for _, block := range blocks {
		if block.String("type") == "tool_use" {
			name, e := protocol.JavaScriptStringJSON(block.Get("name"))
			if e != nil {
				return e
			}
			message, e := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("Side questions cannot call "), name}, "")
			if e != nil {
				return e
			}
			return &MessageError{message}
		}
	}
	return nil
}
