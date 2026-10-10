package openaiauth

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Source stores the last twenty UTF-16 code units, without trimming. The live
// global config is authoritative; this pure lookup does not approve any key.
func IsCustomAPIKeyApproved(global protocol.JSONObject, apiKey json.RawMessage) (bool, error) {
	units, err := protocol.DecodeJavaScriptJSONString(apiKey)
	if err != nil {
		return false, err
	}
	if len(units) > 20 {
		units = units[len(units)-20:]
	}
	var responses protocol.JSONObject
	if err := responses.UnmarshalJSON(global.Get("customApiKeyResponses")); err != nil || !responses.IsObject() {
		return false, nil
	}
	approved := responses.Get("approved")
	if len(approved) == 0 || string(approved) == "null" {
		return false, nil
	}
	if text, err := protocol.DecodeJavaScriptJSONString(approved); err == nil {
		for i := 0; i+len(units) <= len(text); i++ {
			if equalAPIKeyUnits(text[i:i+len(units)], units) {
				return true, nil
			}
		}
		return false, nil
	}
	if !bytes.HasPrefix(bytes.TrimSpace(approved), []byte("[")) {
		return false, errors.New("customApiKeyResponses.approved.includes is not a function")
	}
	var values []json.RawMessage
	if err := json.Unmarshal(approved, &values); err != nil {
		return false, err
	}
	for _, value := range values {
		candidate, err := protocol.DecodeJavaScriptJSONString(value)
		if err == nil && equalAPIKeyUnits(candidate, units) {
			return true, nil
		}
	}
	return false, nil
}

func equalAPIKeyUnits(left, right []uint16) bool {
	if len(left) != len(right) {
		return false
	}
	for i, value := range left {
		if right[i] != value {
			return false
		}
	}
	return true
}
