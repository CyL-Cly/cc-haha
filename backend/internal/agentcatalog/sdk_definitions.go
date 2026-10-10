package agentcatalog

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// AppendSDKDefinitions implements parseAgentsFromJson followed by agents.push.
// A bad record invalidates the complete map. SDK additions retain their slots,
// including collisions with the already resolved active filesystem/CLI agents.
func (s *Service) AppendSDKDefinitions(scope *cooperative.Scope, value json.RawMessage) error {
	if err := s.check(scope); err != nil {
		return err
	}
	var records protocol.JSONObject
	if err := records.UnmarshalJSON(value); err != nil || !records.IsObject() {
		return nil
	}
	memory, err := s.autoMemory(scope)
	if err != nil {
		return err
	}
	definitions := []Definition{}
	for _, field := range records.OwnProperties() {
		var name string
		if err = json.Unmarshal(field.NameJSON, &name); err != nil {
			return err
		}
		var input protocol.JSONObject
		if input.UnmarshalJSON(field.Value) != nil || !input.IsObject() {
			return nil
		}
		for _, key := range []string{"description", "prompt", "model", "initialPrompt"} {
			if value := input.Get(key); value != nil {
				if _, err := protocol.DecodeJavaScriptJSONString(value); err != nil {
					return nil
				}
			}
		}
		definition, err := ParseJSONDefinition(name, field.Value, "flagSettings", s.options.Environment("USER_TYPE"), memory)
		if err != nil || definition == nil {
			return nil
		}
		// Zod's record producer does not assign __proto__ to its parsed object.
		if name == "__proto__" {
			continue
		}
		definitions = append(definitions, *definition)
	}
	s.sdk = append(s.sdk, definitions...)
	s.cache = nil
	return nil
}

func (s *Service) resultWithSDK(all []Definition, failed []Failure) *Result {
	active := append(Active(all), s.sdk...)
	return &Result{All: append(append([]Definition{}, all...), s.sdk...), Active: active, FailedFiles: failed}
}
