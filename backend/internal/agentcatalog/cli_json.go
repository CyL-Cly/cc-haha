package agentcatalog

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// InstallCLIJSON is Source main's safeParseJSON/parseAgentsFromJson producer.
// An invalid record rejects the whole CLI map without changing filesystem
// definitions; CLI additions use normal catalog source precedence, not SDK push.
func (s *Service) InstallCLIJSON(scope *cooperative.Scope, value json.RawMessage) error {
	if err := s.check(scope); err != nil {
		return err
	}
	var records protocol.JSONObject
	if records.UnmarshalJSON(value) != nil || !records.IsObject() {
		return s.InstallCLIDefinitions(scope, nil)
	}
	memory, err := s.autoMemory(scope)
	if err != nil {
		return err
	}
	definitions := []Definition{}
	for _, field := range records.OwnProperties() {
		var name string
		if err := json.Unmarshal(field.NameJSON, &name); err != nil {
			return err
		}
		var input protocol.JSONObject
		if input.UnmarshalJSON(field.Value) != nil || !input.IsObject() {
			return s.InstallCLIDefinitions(scope, nil)
		}
		for _, key := range []string{"description", "prompt", "model", "initialPrompt"} {
			if item := input.Get(key); item != nil {
				if _, err := protocol.DecodeJavaScriptJSONString(item); err != nil {
					return s.InstallCLIDefinitions(scope, nil)
				}
			}
		}
		definition, err := ParseJSONDefinition(name, field.Value, "flagSettings", s.options.Environment("USER_TYPE"), memory)
		if err != nil || definition == nil {
			return s.InstallCLIDefinitions(scope, nil)
		}
		if name != "__proto__" {
			definitions = append(definitions, *definition)
		}
	}
	return s.InstallCLIDefinitions(scope, definitions)
}
