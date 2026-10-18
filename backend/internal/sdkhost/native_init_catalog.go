package sdkhost

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

// NativeInitCatalog is the immutable QueryEngine Promise.all result. Commands
// and agents still come from the actual accepted request's captured registry.
type NativeInitCatalog struct {
	Skills  []json.RawMessage
	Plugins []json.RawMessage
}

// NativeSlashCommandSkillNames combines the original getSlashCommandToolSkills
// predicate with buildSystemInitMessage's userInvocable projection. It retains
// the native loader's quoted names, including JavaScript UTF-16 identities.
func NativeSlashCommandSkillNames(definitions []skillcommands.Definition) []json.RawMessage {
	result := []json.RawMessage{}
	for _, definition := range definitions {
		metadata := definition.Metadata()
		if metadata.String("type") != "prompt" || metadata.String("source") == "builtin" || string(metadata.Get("userInvocable")) == "false" {
			continue
		}
		if string(metadata.Get("hasUserSpecifiedDescription")) != "true" && !hookRawTruthy(metadata.Get("whenToUse")) {
			continue
		}
		loaded := metadata.String("loadedFrom")
		if loaded != "skills" && loaded != "plugin" && loaded != "bundled" && string(metadata.Get("disableModelInvocation")) != "true" {
			continue
		}
		result = append(result, append(json.RawMessage(nil), commandStringJSON(metadata.Get("name"), definition.Name())...))
	}
	return result
}
