package agentcatalog

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ParseJSONDefinition ports parseAgentFromJson for an already-owned CLI/SDK
// value. Execution and lazy memory/MCP/skill/hook activation use this same
// Definition, rather than rediscovering a mutable global agent file.
func ParseJSONDefinition(name string, data json.RawMessage, source, userType string, memoryEnabled bool) (*Definition, error) {
	var input protocol.JSONObject
	if input.UnmarshalJSON(data) != nil || !input.IsObject() {
		return nil, nil
	}
	getString := func(key string, trim, required bool) (string, bool) {
		raw := input.Get(key)
		if raw == nil {
			return "", !required
		}
		value, e := exactString(raw)
		if e != nil {
			return "", false
		}
		if trim {
			value = trimJS(value)
		}
		if required && value == "" {
			return "", false
		}
		return value, true
	}
	description, ok := getString("description", true, true)
	if !ok {
		return nil, nil
	}
	// Zod trims JavaScript code units. A JSON prompt can contain unpaired
	// surrogates even though a filesystem/listing string cannot; preserve the
	// captured getSystemPrompt value independently of its display projection.
	promptUnits, promptErr := protocol.DecodeJavaScriptJSONString(input.Get("prompt"))
	if promptErr != nil {
		return nil, nil
	}
	for len(promptUnits) > 0 && protocol.IsJavaScriptWhitespace(rune(promptUnits[0])) {
		promptUnits = promptUnits[1:]
	}
	for len(promptUnits) > 0 && protocol.IsJavaScriptWhitespace(rune(promptUnits[len(promptUnits)-1])) {
		promptUnits = promptUnits[:len(promptUnits)-1]
	}
	if len(promptUnits) == 0 {
		return nil, nil
	}
	promptJSON := protocol.QuoteJavaScriptUTF16(promptUnits)
	var prompt string
	if err := json.Unmarshal(promptJSON, &prompt); err != nil {
		return nil, err
	}
	for _, field := range []string{"tools", "disallowedTools", "skills"} {
		if data := input.Get(field); data != nil {
			var items []json.RawMessage
			if len(data) == 0 || data[0] != '[' || json.Unmarshal(data, &items) != nil {
				return nil, nil
			}
			for _, item := range items {
				if _, e := protocol.DecodeJavaScriptJSONString(item); e != nil {
					return nil, nil
				}
			}
		}
	}
	model, ok := getString("model", true, false)
	if !ok || input.Get("model") != nil && model == "" {
		return nil, nil
	}
	if strings.EqualFold(model, "inherit") {
		model = "inherit"
	}
	if data := input.Get("effort"); data != nil {
		var level string
		if json.Unmarshal(data, &level) == nil && string(data) != "null" {
			if level != "low" && level != "medium" && level != "high" && level != "xhigh" && level != "max" {
				return nil, nil
			}
		} else {
			var n float64
			if string(data) == "null" || json.Unmarshal(data, &n) != nil || math.Trunc(n) != n || math.Abs(n) > 9007199254740991 || math.IsInf(n, 0) {
				return nil, nil
			}
		}
	}
	for _, field := range []string{"initialPrompt"} {
		if _, ok = getString(field, false, false); !ok {
			return nil, nil
		}
	}
	contains := func(value string, allowed ...string) bool {
		for _, item := range allowed {
			if item == value {
				return true
			}
		}
		return false
	}
	for field, allowed := range map[string][]string{"permissionMode": {"default", "acceptEdits", "bypassPermissions", "dontAsk", "plan", "auto"}, "memory": {"user", "project", "local"}, "color": {"red", "blue", "green", "yellow", "purple", "orange", "pink", "cyan"}, "isolation": {"worktree"}} {
		if field == "isolation" && userType == "ant" {
			allowed = append(allowed, "remote")
		}
		if data := input.Get(field); data != nil {
			if !contains(input.String(field), allowed...) {
				return nil, nil
			}
		}
	}
	for _, field := range []string{"background", "omitClaudeMd"} {
		if data := input.Get(field); data != nil && string(data) != "true" && string(data) != "false" {
			return nil, nil
		}
	}
	if data := input.Get("maxTurns"); data != nil {
		var n float64
		if string(data) == "null" || json.Unmarshal(data, &n) != nil || math.Trunc(n) != n || n <= 0 || n > 9007199254740991 || math.IsInf(n, 0) {
			return nil, nil
		}
	}
	var mcp []json.RawMessage
	if data := input.Get("mcpServers"); data != nil {
		if len(data) == 0 || data[0] != '[' || json.Unmarshal(data, &mcp) != nil {
			return nil, nil
		}
		for i, item := range mcp {
			if _, e := protocol.DecodeJavaScriptJSONString(item); e == nil {
				continue
			}
			var spec protocol.JSONObject
			if spec.UnmarshalJSON(item) != nil || !spec.IsObject() {
				return nil, nil
			}
			parsed := protocol.NewJSONObject()
			for _, p := range spec.OwnProperties() {
				if string(p.NameJSON) == `"__proto__"` {
					continue
				}
				server, issues := mcpconfig.ValidateServer(p.Value)
				if len(issues) > 0 {
					return nil, nil
				}
				_ = parsed.SetPropertyJSON(p.NameJSON, encodeField(server))
			}
			mcp[i] = encodeField(parsed)
		}
	}
	var hooks json.RawMessage
	if data := input.Get("hooks"); data != nil {
		hooks = parseDefinitionHooks(data)
		if hooks == nil {
			return nil, nil
		}
	}
	tools, e := parsedAgentTools(input.Get("tools"))
	if e != nil {
		return nil, e
	}
	if memoryEnabled && input.String("memory") != "" && tools != nil {
		for _, tool := range []string{"Write", "Edit", "Read"} {
			if !contains(tool, tools...) {
				tools = append(tools, tool)
			}
		}
	}
	disallowed, e := parsedAgentTools(input.Get("disallowedTools"))
	if e != nil {
		return nil, e
	}
	fields := protocol.NewJSONObject()
	fields.Set("agentType", encodeField(name))
	fields.Set("whenToUse", encodeField(description))
	fields.Set("rawSystemPrompt", promptJSON)
	if tools != nil {
		fields.Set("tools", encodeField(tools))
	}
	if disallowed != nil {
		fields.Set("disallowedTools", encodeField(disallowed))
	}
	fields.Set("source", encodeField(source))
	if model != "" {
		fields.Set("model", encodeField(model))
	}
	if input.Get("effort") != nil {
		fields.Set("effort", input.Get("effort"))
	}
	if input.Get("permissionMode") != nil {
		fields.Set("permissionMode", input.Get("permissionMode"))
	}
	if len(mcp) > 0 {
		fields.Set("mcpServers", encodeField(mcp))
	}
	if hooks != nil {
		fields.Set("hooks", hooks)
	}
	if input.Get("maxTurns") != nil {
		fields.Set("maxTurns", input.Get("maxTurns"))
	}
	var skills []string
	_ = json.Unmarshal(input.Get("skills"), &skills)
	if len(skills) > 0 {
		fields.Set("skills", input.Get("skills"))
	}
	if input.String("initialPrompt") != "" {
		fields.Set("initialPrompt", input.Get("initialPrompt"))
	}
	if string(input.Get("background")) == "true" {
		fields.Set("background", []byte(`true`))
	}
	for _, field := range []string{"omitClaudeMd", "memory", "isolation", "color"} {
		if input.Get(field) != nil {
			fields.Set(field, input.Get(field))
		}
	}
	return &Definition{Name: name, Description: description, Model: model, Memory: input.String("memory"), Source: source, Body: prompt, SystemPrompt: prompt, promptJSON: append(json.RawMessage(nil), promptJSON...), Fields: fields, PromptArity: 0, Effort: input.Get("effort")}, nil
}
