package agentcatalog

import (
	"encoding/json"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentbuiltinmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func encodeField(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return protocol.StringifyValidatedJavaScriptJSON(raw)
}

// ParseRawTools follows markdownConfigLoader's missing/inherit distinction,
// and the CLI parser's comma/ASCII-space separators outside parentheses.
func ParseRawTools(raw json.RawMessage) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	values := []json.RawMessage{}
	if _, err := protocol.DecodeJavaScriptJSONString(raw); err == nil {
		values = append(values, raw)
	} else if len(raw) > 0 && raw[0] == '[' {
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, err
		}
	}
	items := []string{}
	for _, value := range values {
		if _, err := protocol.DecodeJavaScriptJSONString(value); err != nil {
			continue
		}
		text, err := exactString(value)
		if err != nil {
			return nil, err
		}
		var current strings.Builder
		parens := false
		push := func() {
			if rule := trimJS(current.String()); rule != "" {
				items = append(items, rule)
			}
			current.Reset()
		}
		for _, character := range text {
			switch character {
			case '(':
				parens = true
				current.WriteRune(character)
			case ')':
				parens = false
				current.WriteRune(character)
			case ',', ' ':
				if parens {
					current.WriteRune(character)
				} else {
					push()
				}
			default:
				current.WriteRune(character)
			}
		}
		push()
	}
	return items, nil
}

func parsedAgentTools(raw json.RawMessage) ([]string, error) {
	values, err := ParseRawTools(raw)
	for _, value := range values {
		if value == "*" {
			return nil, err
		}
	}
	return values, err
}

func positiveFrontmatterInt(raw json.RawMessage) json.RawMessage {
	if raw == nil || string(raw) == "null" {
		return nil
	}
	var number float64
	if json.Unmarshal(raw, &number) != nil {
		text, err := protocol.JavaScriptStringJSON(raw)
		if err != nil {
			return nil
		}
		value, err := exactString(text)
		if err != nil {
			return nil
		}
		value = trimJS(value)
		i := 0
		if len(value) > 0 && (value[0] == '-' || value[0] == '+') {
			i++
		}
		start := i
		for i < len(value) && value[i] >= '0' && value[i] <= '9' {
			i++
		}
		if i == start {
			return nil
		}
		number, _ = strconv.ParseFloat(value[:i], 64)
	}
	if number <= 0 || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number {
		return nil
	}
	return encodeField(number)
}

func enrichMarkdownDefinition(d *Definition, memoryEnabled bool) error {
	f := d.Frontmatter
	fields := protocol.NewJSONObject()
	baseDir := d.BaseDir
	if baseDir == "" {
		baseDir = filepath.Dir(d.FilePath)
	}
	fields.Set("baseDir", encodeField(baseDir))
	fields.Set("sourceFilePath", encodeField(d.FilePath))
	if d.ContentHash != "" {
		fields.Set("sourceContentHash", encodeField(d.ContentHash))
	}
	fields.Set("agentType", encodeField(d.Name))
	fields.Set("whenToUse", encodeField(d.Description))
	fields.Set("rawSystemPrompt", encodeField(d.Body))
	rawTools, err := ParseRawTools(f.Get("tools"))
	if err != nil {
		return err
	}
	if rawTools != nil {
		fields.Set("rawTools", encodeField(rawTools))
	}
	tools, err := parsedAgentTools(f.Get("tools"))
	if err != nil {
		return err
	}
	if tools != nil && d.Memory != "" && memoryEnabled {
		for _, name := range []string{"Write", "Edit", "Read"} {
			found := false
			for _, tool := range tools {
				found = found || tool == name
			}
			if !found {
				tools = append(tools, name)
			}
		}
	}
	if tools != nil {
		fields.Set("tools", encodeField(tools))
	}
	if disallowed, err := parsedAgentTools(f.Get("disallowedTools")); err != nil {
		return err
	} else if disallowed != nil {
		fields.Set("disallowedTools", encodeField(disallowed))
	}
	skills, err := ParseRawTools(f.Get("skills"))
	if err != nil {
		return err
	}
	if skills == nil {
		skills = []string{}
	}
	fields.Set("skills", encodeField(skills))
	if initial := f.Get("initialPrompt"); initial != nil {
		if text, err := exactString(initial); err != nil {
			return err
		} else if trimJS(text) != "" {
			fields.Set("initialPrompt", initial)
		}
	}
	if raw := f.Get("mcpServers"); raw != nil {
		var specifications []json.RawMessage
		if json.Unmarshal(raw, &specifications) == nil {
			accepted := []json.RawMessage{}
			for _, specification := range specifications {
				if _, err := protocol.DecodeJavaScriptJSONString(specification); err == nil {
					accepted = append(accepted, specification)
					continue
				}
				var record protocol.JSONObject
				if record.UnmarshalJSON(specification) != nil || !record.IsObject() {
					continue
				}
				next := protocol.NewJSONObject()
				valid := true
				for _, property := range record.OwnProperties() {
					if string(property.NameJSON) == `"__proto__"` {
						continue
					}
					config, issues := mcpconfig.ValidateServer(property.Value)
					if len(issues) != 0 {
						valid = false
						break
					}
					value, _ := config.MarshalJSON()
					_ = next.SetPropertyJSON(property.NameJSON, value)
				}
				if valid {
					value, _ := next.MarshalJSON()
					accepted = append(accepted, value)
				}
			}
			if len(accepted) > 0 {
				fields.Set("mcpServers", encodeField(accepted))
			}
		}
	}
	if hooks := parseDefinitionHooks(f.Get("hooks")); hooks != nil {
		fields.Set("hooks", hooks)
	}
	fields.Set("source", encodeField(d.Source))
	fields.Set("filename", encodeField(strings.TrimSuffix(filepath.Base(d.FilePath), ".md")))
	switch f.String("color") {
	case "red", "blue", "green", "yellow", "purple", "orange", "pink", "cyan":
		fields.Set("color", f.Get("color"))
	}
	if d.Model != "" {
		fields.Set("model", encodeField(d.Model))
	}
	if d.Effort != nil {
		fields.Set("effort", d.Effort)
	}
	switch f.String("permissionMode") {
	case "default", "acceptEdits", "bypassPermissions", "dontAsk", "plan", "auto":
		fields.Set("permissionMode", f.Get("permissionMode"))
	}
	if turns := positiveFrontmatterInt(f.Get("maxTurns")); turns != nil {
		fields.Set("maxTurns", turns)
	}
	if string(f.Get("background")) == "true" || f.String("background") == "true" {
		fields.Set("background", []byte("true"))
	}
	if d.Memory != "" {
		fields.Set("memory", encodeField(d.Memory))
	}
	if f.String("isolation") == "worktree" {
		fields.Set("isolation", f.Get("isolation"))
	}
	d.Fields, d.SystemPrompt, d.PromptArity = fields, d.Body, 0
	return nil
}

func (s *Service) autoMemory(scope *cooperative.Scope) (bool, error) {
	if s.options.AutoMemoryEnabled != nil {
		return s.options.AutoMemoryEnabled(scope)
	}
	value := s.options.Environment("CLAUDE_CODE_DISABLE_AUTO_MEMORY")
	if boolEnv(value) {
		return false, nil
	}
	switch strings.ToLower(trimJS(value)) {
	case "0", "false", "no", "off":
		return true, nil
	}
	if boolEnv(s.options.Environment("CLAUDE_CODE_SIMPLE")) || boolEnv(s.options.Environment("CLAUDE_CODE_REMOTE")) && s.options.Environment("CLAUDE_CODE_REMOTE_MEMORY_DIR") == "" {
		return false, nil
	}
	return true, nil
}

func (s *Service) Prompt(scope *cooperative.Scope, definition Definition, guide agentbuiltinmetadata.GuideContext) (string, error) {
	if err := s.check(scope); err != nil {
		return "", err
	}
	if definition.Source == "built-in" && definition.Name == "claude-code-guide" {
		return agentbuiltinmetadata.GuidePrompt(guide), nil
	}
	prompt := definition.SystemPrompt
	if definition.Memory == "" {
		return prompt, nil
	}
	enabled, err := s.autoMemory(scope)
	if err != nil || !enabled {
		return prompt, err
	}
	if s.options.MemoryPrompt == nil {
		return "", cap("actual agent memory prompt producer")
	}
	memory, err := s.options.MemoryPrompt(scope, definition.Name, definition.Memory)
	if err != nil {
		return "", err
	}
	return prompt + "\n\n" + memory, nil
}

// PromptJSON is the same owner-bound producer with a quoted request result.
// Ordinary agents retain the existing prompt/memory path unchanged.
func (s *Service) PromptJSON(scope *cooperative.Scope, definition Definition, guide agentbuiltinmetadata.GuideContext) (json.RawMessage, error) {
	if definition.Source == "built-in" && definition.Name == "claude-code-guide" {
		if err := s.check(scope); err != nil {
			return nil, err
		}
		return agentbuiltinmetadata.GuidePromptJSON(guide)
	}
	prompt, err := s.Prompt(scope, definition, guide)
	if err != nil {
		return nil, err
	}
	return quoteAgentPrompt(prompt), nil
}
func quoteAgentPrompt(prompt string) json.RawMessage { raw, _ := json.Marshal(prompt); return raw }
