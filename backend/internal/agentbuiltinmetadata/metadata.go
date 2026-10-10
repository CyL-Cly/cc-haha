// Package agentbuiltinmetadata owns the original shipped agent declarations.
// Both the desktop API and native agent producer consume these same definitions.
package agentbuiltinmetadata

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

//go:embed stock.json
var stockJSON []byte

type Definition struct {
	Fields       protocol.JSONObject
	SystemPrompt string
	PromptArity  int
}

func Stock() []Definition {
	var rows []struct {
		Fields       json.RawMessage
		SystemPrompt string
		PromptArity  int
	}
	if err := json.Unmarshal(stockJSON, &rows); err != nil {
		panic(err)
	}
	out := make([]Definition, 0, len(rows))
	for _, row := range rows {
		var fields protocol.JSONObject
		if err := fields.UnmarshalJSON(row.Fields); err != nil {
			panic(err)
		}
		out = append(out, Definition{fields, row.SystemPrompt, row.PromptArity})
	}
	return out
}

type GuideContext struct {
	Commands, Agents, MCPServers []protocol.JSONObject
	Settings                     protocol.JSONObject
	Using3P                      bool
	IssuesExplainer              string
}

func stringField(object protocol.JSONObject, key string) string {
	raw, _ := protocol.JavaScriptStringJSON(object.Get(key))
	var text string
	_ = json.Unmarshal(raw, &text)
	return text
}

const ordinaryFeedback = "- When you cannot find an answer or the feature doesn't exist, direct the user to use /feedback to report a feature request or bug"

func GuidePrompt(context GuideContext) string {
	var baseline string
	for _, definition := range Stock() {
		if definition.Fields.String("agentType") == "claude-code-guide" {
			baseline = definition.SystemPrompt
			break
		}
	}
	baseline = strings.TrimSuffix(baseline, ordinaryFeedback)
	if context.Using3P {
		baseline += "- When you cannot find an answer or the feature doesn't exist, direct the user to " + context.IssuesExplainer
	} else {
		baseline += ordinaryFeedback
	}
	sections := []string{}
	commands, plugins := []string{}, []string{}
	for _, command := range context.Commands {
		if command.String("type") == "prompt" {
			line := "- /" + stringField(command, "name") + ": " + stringField(command, "description")
			commands = append(commands, line)
			if command.String("source") == "plugin" {
				plugins = append(plugins, line)
			}
		}
	}
	if len(commands) > 0 {
		sections = append(sections, "**Available custom skills in this project:**\n"+strings.Join(commands, "\n"))
	}
	agents := []string{}
	for _, agent := range context.Agents {
		if agent.String("source") != "built-in" {
			agents = append(agents, "- "+stringField(agent, "agentType")+": "+stringField(agent, "whenToUse"))
		}
	}
	if len(agents) > 0 {
		sections = append(sections, "**Available custom agents configured:**\n"+strings.Join(agents, "\n"))
	}
	servers := []string{}
	for _, server := range context.MCPServers {
		servers = append(servers, "- "+stringField(server, "name"))
	}
	if len(servers) > 0 {
		sections = append(sections, "**Configured MCP servers:**\n"+strings.Join(servers, "\n"))
	}
	if len(plugins) > 0 {
		sections = append(sections, "**Available plugin skills:**\n"+strings.Join(plugins, "\n"))
	}
	if len(context.Settings.OwnProperties()) > 0 {
		raw, _ := context.Settings.MarshalJSON()
		canonical, _ := protocol.ParseAndStringifyJavaScriptJSON(raw)
		var formatted bytes.Buffer
		_ = json.Indent(&formatted, canonical, "", "  ")
		sections = append(sections, "**User's settings.json:**\n"+string(rune(96))+string(rune(96))+string(rune(96))+"json\n"+formatted.String()+"\n"+string(rune(96))+string(rune(96))+string(rune(96)))
	}
	if len(sections) == 0 {
		return baseline
	}
	return baseline + "\n\n---\n\n# User's Current Configuration\n\nThe user has the following custom setup in their environment:\n\n" + strings.Join(sections, "\n\n") + "\n\nWhen answering questions, consider these configured features and proactively suggest them when relevant."
}

// GuidePromptJSON keeps dynamic declaration values as JavaScript code units
// through the actual model request. The legacy string formatter remains for
// display callers; it is not an identity or request serialization boundary.
func GuidePromptJSON(context GuideContext) (json.RawMessage, error) {
	literal := func(text string) json.RawMessage { return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(text))) }
	var failure error
	join := func(parts ...json.RawMessage) json.RawMessage {
		result, err := protocol.JoinJavaScriptValuesJSON(parts, "")
		if err != nil {
			failure = err
		}
		return result
	}
	field := func(object protocol.JSONObject, key string) json.RawMessage {
		result, err := protocol.JavaScriptStringJSON(object.Get(key))
		if err != nil {
			failure = err
		}
		return result
	}
	lines := func(values []json.RawMessage) json.RawMessage {
		result, err := protocol.JoinJavaScriptValuesJSON(values, "\n")
		if err != nil {
			failure = err
		}
		return result
	}
	baseline := ""
	for _, definition := range Stock() {
		if definition.Fields.String("agentType") == "claude-code-guide" {
			baseline = definition.SystemPrompt
			break
		}
	}
	baseline = strings.TrimSuffix(baseline, ordinaryFeedback)
	if context.Using3P {
		baseline += "- When you cannot find an answer or the feature doesn't exist, direct the user to " + context.IssuesExplainer
	} else {
		baseline += ordinaryFeedback
	}
	sections := []json.RawMessage{}
	commands, plugins := []json.RawMessage{}, []json.RawMessage{}
	for _, command := range context.Commands {
		if command.String("type") == "prompt" {
			line := join(literal("- /"), field(command, "name"), literal(": "), field(command, "description"))
			commands = append(commands, line)
			if command.String("source") == "plugin" {
				plugins = append(plugins, line)
			}
		}
	}
	if len(commands) > 0 {
		sections = append(sections, join(literal("**Available custom skills in this project:**\n"), lines(commands)))
	}
	agents := []json.RawMessage{}
	for _, agent := range context.Agents {
		if agent.String("source") != "built-in" {
			agents = append(agents, join(literal("- "), field(agent, "agentType"), literal(": "), field(agent, "whenToUse")))
		}
	}
	if len(agents) > 0 {
		sections = append(sections, join(literal("**Available custom agents configured:**\n"), lines(agents)))
	}
	servers := []json.RawMessage{}
	for _, server := range context.MCPServers {
		servers = append(servers, join(literal("- "), field(server, "name")))
	}
	if len(servers) > 0 {
		sections = append(sections, join(literal("**Configured MCP servers:**\n"), lines(servers)))
	}
	if len(plugins) > 0 {
		sections = append(sections, join(literal("**Available plugin skills:**\n"), lines(plugins)))
	}
	if len(context.Settings.OwnProperties()) > 0 {
		raw, err := context.Settings.MarshalJSON()
		if err != nil {
			return nil, err
		}
		canonical, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
		if err != nil {
			return nil, err
		}
		var formatted bytes.Buffer
		if err = json.Indent(&formatted, canonical, "", "  "); err != nil {
			return nil, err
		}
		sections = append(sections, literal("**User's settings.json:**\n```json\n"+formatted.String()+"\n```"))
	}
	if failure != nil {
		return nil, failure
	}
	if len(sections) == 0 {
		return literal(baseline), nil
	}
	body, err := protocol.JoinJavaScriptValuesJSON(sections, "\n\n")
	if err != nil {
		return nil, err
	}
	result := join(literal(baseline+"\n\n---\n\n# User's Current Configuration\n\nThe user has the following custom setup in their environment:\n\n"), body, literal("\n\nWhen answering questions, consider these configured features and proactively suggest them when relevant."))
	return result, failure
}
