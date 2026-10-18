package sdkhost

import (
	"encoding/json"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

// Command metadata is ported from shipped Source declarations, not SDK captures.
// This finite catalog is deliberately marked incomplete by PrintRegistry.
type printCommandDefinition struct {
	name, description, hint, kind, source, userFacingName string
	promptKind                                            string
	pluginManifestName                                    json.RawMessage
	rawName, rawDescription, rawUserFacingName            json.RawMessage
	aliases                                               []string
	argNames                                              []string
	userInvocable                                         *bool
	supportsNonInteractive, disableNonInteractive         bool
	migratedExecutor                                      bool
	hidden                                                bool
}

func migratedPrintCommands() []printCommandDefinition {
	result := []printCommandDefinition{}
	for _, definition := range compiledPrintCommandDeclarations() {
		if definition.migratedExecutor {
			result = append(result, definition)
		}
	}
	return result
}

// Both consumers use this Source declaration table. Cold menu metadata does
// not mark a command's runtime executor migrated or allocate a Session.
func compiledPrintCommandDeclarations() []printCommandDefinition {
	agent := skillcommands.AgentCommandMetadata()
	prComments := skillcommands.PRCommentsCommandMetadata()
	init := skillcommands.InitCommandMetadata(false)
	securityReview := skillcommands.SecurityReviewCommandMetadata()
	statusline := skillcommands.StatuslineCommandMetadata()
	return []printCommandDefinition{
		{name: agent.String("name"), description: agent.String("description"), hint: agent.String("argumentHint"), kind: agent.String("type"), source: agent.String("source")},
		{name: "clear", description: "Clear conversation history and free up context", kind: "local", aliases: []string{"reset", "new"}, migratedExecutor: true},
		{name: "compact", description: "Clear conversation history but keep a summary in context. Optional: /compact [instructions for summarization]", hint: "<optional custom summarization instructions>", kind: "local", supportsNonInteractive: true, migratedExecutor: true},
		{name: "context", description: "Show current context usage", kind: "local", supportsNonInteractive: true, migratedExecutor: true},
		{name: "cost", description: "Show the total cost and duration of the current session", kind: "local", supportsNonInteractive: true, migratedExecutor: true},
		{name: "effort", description: "Set effort level for model usage", hint: "[low|medium|high|max|auto]", kind: "local-jsx", migratedExecutor: true},
		{name: "heapdump", description: "Dump the JS heap to ~/Desktop", kind: "local", supportsNonInteractive: true, hidden: true, migratedExecutor: true},
		{name: init.String("name"), description: init.String("description"), kind: init.String("type"), source: init.String("source")},
		{name: prComments.String("name"), description: prComments.String("description"), kind: prComments.String("type"), source: prComments.String("source")},
		{name: "release-notes", description: "View release notes", kind: "local", supportsNonInteractive: true, migratedExecutor: true},
		{name: "vim", description: "Toggle between Vim and Normal editing modes", kind: "local", migratedExecutor: true},
		{name: "stickers", description: "Order Claude Code stickers", kind: "local", migratedExecutor: true},
		{name: "goal", description: "Set a completion goal", hint: "[<condition> | clear]", kind: "local-jsx", supportsNonInteractive: true, migratedExecutor: true},
		{name: "plan", description: "Enable plan mode or view the current session plan", hint: "[open|<description>]", kind: "local-jsx", migratedExecutor: true},
		{name: "btw", description: "Ask a quick side question without interrupting the main conversation", hint: "<question>", kind: "local-jsx", migratedExecutor: true},
		{name: "buddy", description: "Meet your companion", hint: "[hatch|pet|mute|unmute|info]", kind: "local-jsx", migratedExecutor: true},
		{name: "review", description: "Review a pull request", kind: "prompt", source: "builtin", migratedExecutor: true},
		{name: securityReview.String("name"), description: securityReview.String("description"), kind: securityReview.String("type"), source: securityReview.String("source")},
		{name: "insights", description: "Generate a report analyzing your Claude Code sessions", kind: "prompt", source: "builtin", migratedExecutor: true},
		{name: statusline.String("name"), description: statusline.String("description"), kind: statusline.String("type"), source: statusline.String("source"), disableNonInteractive: true},
	}
}
func filterHeadlessCommands(all []printCommandDefinition) []printCommandDefinition {
	out := []printCommandDefinition{}
	for _, c := range all {
		supported := c.supportsNonInteractive
		if c.kind == "prompt" {
			supported = !c.disableNonInteractive
		}
		if supported {
			out = append(out, c)
		}
	}
	return out
}

// System init retains raw name/description and truthy hints. Control initialize
// resolves user-facing names/source annotations and emits an empty hint field.
func projectSystemCommands(all []printCommandDefinition) []protocolCommand {
	out := []protocolCommand{}
	for _, c := range all {
		if c.userInvocable == nil || *c.userInvocable {
			out = append(out, protocolCommand{Name: c.name, Description: c.description, ArgumentHint: c.hint, rawName: commandStringJSON(c.rawName, c.name), rawDescription: commandStringJSON(c.rawDescription, c.description)})
		}
	}
	return out
}

type ControlCommand struct {
	Name                    string `json:"name"`
	Description             string `json:"description"`
	ArgumentHint            string `json:"argumentHint"`
	rawName, rawDescription json.RawMessage
	// Terminal metadata comes from the same definitions. The original SDK
	// control protocol deliberately excludes these UI discriminants.
	Kind              string   `json:"-"`
	Sensitive         bool     `json:"-"`
	Aliases           []string `json:"-"`
	RequiresArguments bool     `json:"-"`
	ArgumentNames     []string `json:"-"`
	Hidden            bool     `json:"-"`
}

func projectControlCommands(all []printCommandDefinition) []ControlCommand {
	out := []ControlCommand{}
	for _, c := range all {
		if c.userInvocable != nil && !*c.userInvocable {
			continue
		}
		nameJSON := commandStringJSON(c.rawName, c.name)
		if openaiauth.AuthTruthy(c.rawUserFacingName) {
			nameJSON = c.rawUserFacingName
		} else if c.userFacingName != "" {
			nameJSON = commandStringJSON(nil, c.userFacingName)
		}
		var name string
		_ = json.Unmarshal(nameJSON, &name)
		descriptionJSON := controlCommandDescriptionJSON(c)
		var description string
		_ = json.Unmarshal(descriptionJSON, &description)
		out = append(out, ControlCommand{Name: name, Description: description, ArgumentHint: c.hint, rawName: nameJSON, rawDescription: descriptionJSON, Kind: c.kind, Aliases: append([]string(nil), c.aliases...), RequiresArguments: len(c.argNames) > 0, ArgumentNames: append([]string(nil), c.argNames...), Hidden: c.hidden})
	}
	return out
}

// Source formatDescriptionWithSource annotates only the control/UI view.
// Model-facing metadata and the system-init projection keep the raw text.
func controlCommandDescriptionJSON(c printCommandDefinition) json.RawMessage {
	description := commandStringJSON(c.rawDescription, c.description)
	join := func(parts ...json.RawMessage) json.RawMessage {
		joined, _ := protocol.JoinJavaScriptValuesJSON(parts, "")
		return joined
	}
	if c.kind != "prompt" {
		return description
	}
	if c.promptKind == "workflow" {
		return join(description, quote(" (workflow)"))
	}
	switch c.source {
	case "plugin":
		if openaiauth.AuthTruthy(c.pluginManifestName) {
			// Use the original optional manifest value, never the command name.
			quoted, _ := protocol.JavaScriptStringJSON(c.pluginManifestName)
			return join(quote("("), quoted, quote(") "), description)
		}
		return join(description, quote(" (plugin)"))
	case "builtin", "mcp":
		return description
	case "bundled":
		return join(description, quote(" (bundled)"))
	}
	// Keep getSettingSourceName's exact labels and undefined fallthrough.
	name := "undefined"
	switch c.source {
	case "userSettings":
		name = "user"
	case "projectSettings":
		name = "project"
	case "localSettings":
		name = "project, gitignored"
	case "flagSettings":
		name = "cli flag"
	case "policySettings":
		name = "managed"
	}
	return join(description, quote(" ("+name+")"))
}

// Source refresh replaces currentCommands with getCommands without the initial
// headless filter. Executors are independently capability guarded.
func (s *PrintRegistry) RefreshMigratedCommands() {
	s.commands = filterEnabledPrintCommands(migratedPrintCommands(), s.environment)
	if s.remoteSafe {
		s.commands = filterRemoteSafePrintCommands(s.commands)
	}
	s.catalog.Commands = projectSystemCommands(s.commands)
}

func IsRemoteSafeCommand(name string) bool {
	switch name {
	case "session", "exit", "clear", "help", "theme", "color", "vim", "cost", "usage", "copy", "btw", "feedback", "plan", "keybindings", "statusline", "stickers", "mobile":
		return true
	}
	return false
}
func filterRemoteSafePrintCommands(commands []printCommandDefinition) []printCommandDefinition {
	result := []printCommandDefinition{}
	for _, command := range commands {
		if IsRemoteSafeCommand(command.name) {
			result = append(result, command)
		}
	}
	return result
}
func (s *PrintRegistry) ControlCommands() []ControlCommand {
	if s.slashCommandsDisabled {
		return []ControlCommand{}
	}
	return projectControlCommands(s.commands)
}
func (s *PrintRegistry) requireCostCommand() error {
	if s.slashCommandsDisabled {
		return cap("/cost absent from current real command registry")
	}
	for _, c := range s.commands {
		if c.name == "cost" {
			return nil
		}
	}
	return cap("/cost absent from current real command registry")
}

func filterEnabledPrintCommands(all []printCommandDefinition, env func(string) string) []printCommandDefinition {
	out := []printCommandDefinition{}
	for _, c := range all {
		if c.name == "compact" && env != nil {
			switch strings.ToLower(strings.TrimSpace(env("DISABLE_COMPACT"))) {
			case "1", "true", "yes", "on":
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// Optional raw strings are immutable metadata from the actual producers. Literal
// callers retain their ordinary Go fields; no global string representation changes.
func commandStringJSON(raw json.RawMessage, fallback string) json.RawMessage {
	if raw != nil {
		return raw
	}
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(fallback)))
}
func (c protocolCommand) MarshalJSON() ([]byte, error) {
	object := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: commandStringJSON(c.rawName, c.Name)}, protocol.JSONField{Name: "description", Value: commandStringJSON(c.rawDescription, c.Description)})
	if c.ArgumentHint != "" {
		object.Set("argumentHint", commandStringJSON(nil, c.ArgumentHint))
	}
	return object.MarshalJSON()
}
func (c ControlCommand) MarshalJSON() ([]byte, error) {
	object := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: commandStringJSON(c.rawName, c.Name)}, protocol.JSONField{Name: "description", Value: commandStringJSON(c.rawDescription, c.Description)}, protocol.JSONField{Name: "argumentHint", Value: commandStringJSON(nil, c.ArgumentHint)})
	return object.MarshalJSON()
}
