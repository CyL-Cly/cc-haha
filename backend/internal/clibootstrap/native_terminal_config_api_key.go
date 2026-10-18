package clibootstrap

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"slices"
)

func terminalNormalizedAPIKey(key string) json.RawMessage {
	units, err := protocol.DecodeJavaScriptJSONString(quoteMain(key))
	if err != nil {
		return nil
	}
	if len(units) > 20 {
		units = units[len(units)-20:]
	}
	return json.RawMessage(protocol.QuoteJavaScriptUTF16(units))
}
func (m *MainPrint) setTerminalAPIKeyApproval(approved bool) error {
	env := m.nativeOpenAIEnvironment()
	key := env["ANTHROPIC_API_KEY"]
	if key == "" || env["USER_TYPE"] == "ant" && nativeEnvTruthy(env["COO_RUNNING_ON_HOMESPACE"]) {
		return errors.New("Custom API key is unavailable")
	}
	normalized := terminalNormalizedAPIKey(key)
	return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		responses := mainObj(current.Get("customApiKeyResponses"))
		if !responses.IsObject() {
			responses = protocol.NewJSONObject()
		}
		for _, name := range []string{"approved", "rejected"} {
			var list []json.RawMessage
			if value := responses.Get(name); len(value) > 0 && string(value) != "null" {
				if err := json.Unmarshal(value, &list); err != nil {
					return nil, err
				}
			}
			filtered := []json.RawMessage{}
			for _, value := range list {
				valueUnits, err := protocol.DecodeJavaScriptJSONString(value)
				normalizedUnits, _ := protocol.DecodeJavaScriptJSONString(normalized)
				if err != nil || !slices.Equal(valueUnits, normalizedUnits) {
					filtered = append(filtered, value)
				}
			}
			if approved == (name == "approved") {
				filtered = append(filtered, normalized)
			}
			responses.Set(name, rawMain(filtered))
		}
		next.Set("customApiKeyResponses", rawMain(responses))
		return &next, nil
	})
}
