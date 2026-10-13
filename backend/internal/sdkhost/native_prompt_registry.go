package sdkhost

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

// ReplaceNativePrompts publishes actual immutable command definitions. Builtin
// executors remain separately gated until their own implementations are bound.
func (s *PrintRegistry) ReplaceNativePrompts(prompts []skillcommands.Definition, plugins []json.RawMessage, headless bool) {
	s.prompts = append([]skillcommands.Definition{}, prompts...)
	commands := []printCommandDefinition{}
	s.catalog.Skills = []string{}
	s.catalog.rawSkills = []json.RawMessage{}
	for _, prompt := range prompts {
		metadata := prompt.Metadata()
		user := string(metadata.Get("userInvocable")) != "false"
		var argNames []string
		_ = json.Unmarshal(metadata.Get("argNames"), &argNames)
		pluginInfo, _ := metadata.Object("pluginInfo")
		manifest, _ := pluginInfo.Object("pluginManifest")
		commands = append(commands, printCommandDefinition{name: prompt.Name(), rawName: append(json.RawMessage(nil), metadata.Get("name")...), rawDescription: append(json.RawMessage(nil), metadata.Get("description")...), rawUserFacingName: append(json.RawMessage(nil), metadata.Get("userFacingName")...), description: metadata.String("description"), hint: metadata.String("argumentHint"), argNames: argNames, kind: "prompt", source: metadata.String("source"), promptKind: metadata.String("kind"), pluginManifestName: append(json.RawMessage(nil), manifest.Get("name")...), userFacingName: prompt.UserFacingName(), userInvocable: &user, hidden: string(metadata.Get("isHidden")) == "true"})
		if user && (metadata.Get("hasUserSpecifiedDescription") != nil && string(metadata.Get("hasUserSpecifiedDescription")) == "true" || metadata.Get("whenToUse") != nil) && (metadata.String("loadedFrom") == "skills" || metadata.String("loadedFrom") == "plugin" || metadata.String("loadedFrom") == "bundled" || string(metadata.Get("disableModelInvocation")) == "true") {
			s.catalog.Skills = append(s.catalog.Skills, prompt.Name())
			s.catalog.rawSkills = append(s.catalog.rawSkills, append(json.RawMessage(nil), commandStringJSON(metadata.Get("name"), prompt.Name())...))
		}
	}
	builtins := filterEnabledPrintCommands(migratedPrintCommands(), s.environment)
	if headless {
		builtins = filterHeadlessCommands(builtins)
	}
	s.commands = append(commands, builtins...)
	s.catalog.Commands = projectSystemCommands(s.commands)
	s.catalog.Plugins = append([]json.RawMessage{}, plugins...)
}

func (s *PrintRegistry) Prompt(name string) (skillcommands.Definition, bool) {
	if s.slashCommandsDisabled {
		return skillcommands.Definition{}, false
	}
	for _, prompt := range s.prompts {
		if prompt.Name() == name || prompt.UserFacingName() != "" && prompt.UserFacingName() == name {
			return prompt, true
		}
	}
	return skillcommands.Definition{}, false
}
