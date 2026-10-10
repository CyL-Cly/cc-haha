package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

// CompiledHeadlessCommands projects actual native declarations for a cold
// desktop menu. It invokes no prompt/schema callback, Session or model.
func CompiledHeadlessCommands(environment map[string]string, bundled []skillcommands.Definition) []protocol.JSONObject {
	all := []printCommandDefinition{}
	for _, definition := range bundled {
		metadata := definition.Metadata()
		if metadata.String("source") != "bundled" || string(metadata.Get("userInvocable")) == "false" || string(metadata.Get("disableNonInteractive")) == "true" {
			continue
		}
		all = append(all, printCommandDefinition{name: metadata.String("name"), description: metadata.String("description"), hint: metadata.String("argumentHint"), kind: "prompt"})
	}
	all = append(all, filterEnabledPrintCommands(compiledPrintCommandDeclarations(), func(key string) string { return environment[key] })...)
	out := []protocol.JSONObject{}
	for _, command := range filterHeadlessCommands(all) {
		if command.userInvocable != nil && !*command.userInvocable {
			continue
		}
		value := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quote(command.name)}, protocol.JSONField{Name: "description", Value: quote(command.description)})
		if command.hint != "" {
			value.Set("argumentHint", quote(command.hint))
		}
		out = append(out, value)
	}
	return out
}
