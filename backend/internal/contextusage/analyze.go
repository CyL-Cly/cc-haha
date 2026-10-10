// Package contextusage implements the original context analyser over actual
// native messages, definitions and memory. It owns no conversation or model loop.
package contextusage

import (
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/preflightcore"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

// Counter is the actual countTokensWithFallback producer, not a resolved total.
type Counter func([]protocol.JSONObject, []protocol.JSONObject) (*float64, error)
type Agent struct{ Name, Description, Source string }
type Input struct {
	Model                                               string
	Window, EffectiveWindow                             float64
	TerminalWidth                                       int
	AutoCompact, ToolSearch, LowTrust, Simple, Internal bool
	EstimateOnly                                        bool
	Count                                               Counter
	History, Normalized                                 []protocol.JSONObject
	SystemPrompt                                        []json.RawMessage
	SystemContext                                       protocol.JSONObject
	Memory                                              []usercontext.MemoryFile
	Agents                                              []Agent
	SkillMetadata                                       []protocol.JSONObject
	CommandTotal, CommandIncluded                       int
	Definitions                                         []toolschema.Definition
	Schemas                                             []protocol.JSONObject
	// AttachmentMessages uses the existing attachment projector/factory.
	AttachmentMessages func(json.RawMessage) ([]protocol.JSONObject, error)
	EstimateCanonical  func([]protocol.JSONObject) (float64, error)
}

type Category struct {
	Name     string  `json:"name"`
	Tokens   float64 `json:"tokens"`
	Color    string  `json:"color"`
	Deferred bool    `json:"isDeferred,omitempty"`
}
type Square struct {
	Color      string  `json:"color"`
	Filled     bool    `json:"isFilled"`
	Name       string  `json:"categoryName"`
	Tokens     float64 `json:"tokens"`
	Percentage float64 `json:"percentage"`
	Fullness   float64 `json:"squareFullness"`
}
type Memory struct {
	Path   string  `json:"path"`
	Type   string  `json:"type"`
	Tokens float64 `json:"tokens"`
}
type Detail struct {
	Name   string  `json:"name"`
	Tokens float64 `json:"tokens"`
}
type MCP struct {
	Name   string  `json:"name"`
	Server string  `json:"serverName"`
	Tokens float64 `json:"tokens"`
	Loaded bool    `json:"isLoaded"`
}
type AgentDetail struct {
	Name   string  `json:"agentType"`
	Source string  `json:"source"`
	Tokens float64 `json:"tokens"`
}
type Skill struct {
	Name   string  `json:"name"`
	Source string  `json:"source"`
	Tokens float64 `json:"tokens"`
}
type Skills struct {
	Total    int     `json:"totalSkills"`
	Included int     `json:"includedSkills"`
	Tokens   float64 `json:"tokens"`
	Entries  []Skill `json:"skillFrontmatter"`
}
type Commands struct {
	Total    int     `json:"totalCommands"`
	Included int     `json:"includedCommands"`
	Tokens   float64 `json:"tokens"`
}
type DeferredTool struct {
	Name   string  `json:"name"`
	Tokens float64 `json:"tokens"`
	Loaded bool    `json:"isLoaded"`
}
type Data struct {
	Categories    []Category           `json:"categories"`
	Total         float64              `json:"totalTokens"`
	Max           float64              `json:"maxTokens"`
	RawMax        float64              `json:"rawMaxTokens"`
	Percentage    float64              `json:"percentage"`
	Grid          [][]Square           `json:"gridRows"`
	Model         string               `json:"model"`
	Memory        []Memory             `json:"memoryFiles"`
	MCP           []MCP                `json:"mcpTools"`
	Deferred      *[]DeferredTool      `json:"deferredBuiltinTools,omitempty"`
	SystemTools   *[]Detail            `json:"systemTools,omitempty"`
	Sections      *[]Detail            `json:"systemPromptSections,omitempty"`
	Agents        []AgentDetail        `json:"agents"`
	Commands      *Commands            `json:"slashCommands,omitempty"`
	Skills        *Skills              `json:"skills,omitempty"`
	AutoThreshold *float64             `json:"autoCompactThreshold,omitempty"`
	AutoCompact   bool                 `json:"isAutoCompactEnabled"`
	Breakdown     Breakdown            `json:"messageBreakdown"`
	APIUsage      *protocol.JSONObject `json:"apiUsage"`
}

func quote(v string) json.RawMessage { data, _ := json.Marshal(v); return data }
func object(raw json.RawMessage) protocol.JSONObject {
	var value protocol.JSONObject
	_ = value.UnmarshalJSON(raw)
	return value
}
func round(n float64) float64 { return math.Floor(n + .5) }
func (in Input) count(messages, schemas []protocol.JSONObject) (float64, error) {
	if in.EstimateOnly {
		return preflightcore.RoughAPIRequest(messages, schemas)
	}
	if in.Count == nil {
		return 0, errors.New("context analyser requires original token-count transport")
	}
	n, err := in.Count(messages, schemas)
	if err != nil {
		return 0, err
	}
	if n == nil {
		return 0, nil
	}
	return *n, nil
}
func (in Input) text(content json.RawMessage) (float64, error) {
	return in.count([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quote("user")}, protocol.JSONField{Name: "content", Value: content})}, nil)
}
func rough(raw json.RawMessage) (float64, error) { return preflightcore.RoughContent(raw) }
func deferred(d toolschema.Definition) bool      { return !d.AlwaysLoad && (d.IsMCP || d.ShouldDefer) }
func sectionName(raw json.RawMessage) string {
	var text string
	_ = json.Unmarshal(raw, &text)
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimLeft(line, "#")
		if len(trimmed) < len(line) && strings.HasPrefix(trimmed, " ") {
			return strings.TrimSpace(trimmed)
		}
	}
	first := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			first = line
			break
		}
	}
	units, _ := protocol.DecodeJavaScriptJSONString(quote(first))
	if len(units) > 40 {
		units = append(units[:40:40], '…')
		var clipped string
		_ = json.Unmarshal(protocol.QuoteJavaScriptUTF16(units), &clipped)
		return clipped
	}
	return first
}

func Analyze(in Input) (Data, error) {
	if in.Window <= 0 || len(in.Definitions) != len(in.Schemas) || in.EstimateCanonical == nil || !in.SystemContext.IsObject() {
		return Data{}, errors.New("context analyser requires original model, definitions, context and canonical estimator")
	}
	data := Data{Categories: []Category{}, Model: in.Model, Max: in.Window, RawMax: in.Window, Memory: []Memory{}, MCP: []MCP{}, Agents: []AgentDetail{}, AutoCompact: in.AutoCompact}
	sections := []Detail{}
	systemTokens := 0.0
	for _, part := range in.SystemPrompt {
		var text string
		_ = json.Unmarshal(part, &text)
		if text == "" || text == "__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__" {
			continue
		}
		count, err := in.text(part)
		if err != nil {
			return Data{}, err
		}
		sections = append(sections, Detail{sectionName(part), count})
		systemTokens += count
	}
	for _, part := range in.SystemContext.OwnProperties() {
		var name, text string
		_ = json.Unmarshal(part.NameJSON, &name)
		_ = json.Unmarshal(part.Value, &text)
		if text == "" {
			continue
		}
		count, err := in.text(part.Value)
		if err != nil {
			return Data{}, err
		}
		sections = append(sections, Detail{name, count})
		systemTokens += count
	}
	memoryTokens := 0.0
	if !in.Simple {
		for _, file := range in.Memory {
			count, err := in.text(file.PromptContent())
			if err != nil {
				return Data{}, err
			}
			memoryTokens += count
			data.Memory = append(data.Memory, Memory{file.Path, file.Type, count})
		}
	}
	agentTokens := 0.0
	for _, agent := range in.Agents {
		if agent.Source == "built-in" {
			continue
		}
		count, err := in.text(quote(agent.Name + " " + agent.Description))
		if err != nil {
			return Data{}, err
		}
		agentTokens += count
		data.Agents = append(data.Agents, AgentDetail{agent.Name, agent.Source, count})
	}
	used := map[string]bool{}
	for _, message := range in.History {
		if message.String("type") != "assistant" {
			continue
		}
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(object(message.Get("message")).Get("content"), &blocks)
		for _, block := range blocks {
			if block.String("type") == "tool_use" {
				used[block.String("name")] = true
			}
		}
	}
	builtin, blockedBuiltin, mcp := []protocol.JSONObject{}, []protocol.JSONObject{}, []protocol.JSONObject{}
	mcpIndices, deferredIndices := []int{}, []int{}
	skillIndex := -1
	for i, definition := range in.Definitions {
		if definition.Name == "Skill" {
			skillIndex = i
		}
		if definition.IsMCP {
			mcp = append(mcp, in.Schemas[i])
			mcpIndices = append(mcpIndices, i)
		} else if deferred(definition) {
			blockedBuiltin = append(blockedBuiltin, in.Schemas[i])
			deferredIndices = append(deferredIndices, i)
		} else {
			builtin = append(builtin, in.Schemas[i])
		}
	}
	builtinTokens := 0.0
	var err error
	if len(builtin) > 0 {
		builtinTokens, err = in.count(nil, builtin)
		if err != nil {
			return Data{}, err
		}
	}
	deferredDetails := []DeferredTool{}
	deferredBuiltinTokens := 0.0
	if len(blockedBuiltin) > 0 {
		if !in.ToolSearch {
			count, err := in.count(nil, blockedBuiltin)
			if err != nil {
				return Data{}, err
			}
			builtinTokens += count
		} else {
			for _, index := range deferredIndices {
				count, err := in.count(nil, []protocol.JSONObject{in.Schemas[index]})
				if err != nil {
					return Data{}, err
				}
				count = math.Max(0, count-500)
				loaded := used[in.Definitions[index].Name]
				deferredDetails = append(deferredDetails, DeferredTool{in.Definitions[index].Name, count, loaded})
				if loaded {
					builtinTokens += count
				} else {
					deferredBuiltinTokens += count
				}
			}
		}
	}
	mcpRaw, err := in.count(nil, mcp)
	if err != nil {
		return Data{}, err
	}
	mcpTotal := math.Max(0, mcpRaw-500)
	estimates := []float64{}
	estimateTotal := 0.0
	for _, index := range mcpIndices {
		schema := in.Schemas[index]
		// Source estimates proportions from the tool's name, prompt and input
		// schema. Transport flags such as strict/defer_loading are excluded.
		proportion := protocol.NewJSONObject()
		for _, name := range []string{"name", "description", "input_schema"} {
			if schema.Get(name) != nil {
				proportion.Set(name, schema.Get(name))
			}
		}
		raw, err := proportion.MarshalJSON()
		if err != nil {
			return Data{}, err
		}
		raw = protocol.StringifyValidatedJavaScriptJSON(raw)
		count, err := rough(quote(string(raw)))
		if err != nil {
			return Data{}, err
		}
		estimates = append(estimates, count)
		estimateTotal += count
	}
	if estimateTotal == 0 {
		estimateTotal = 1
	}
	mcpLoaded, mcpDeferred := 0.0, 0.0
	for i, index := range mcpIndices {
		definition := in.Definitions[index]
		count := round(estimates[i] / estimateTotal * mcpTotal)
		parts := strings.Split(definition.Name, "__")
		server := "unknown"
		if len(parts) > 1 && parts[1] != "" {
			server = parts[1]
		}
		loaded := in.ToolSearch && used[definition.Name] || !deferred(definition)
		data.MCP = append(data.MCP, MCP{definition.Name, server, count, loaded})
		if loaded {
			mcpLoaded += count
		} else if in.ToolSearch {
			mcpDeferred += count
		}
	}
	mcpTokens := mcpTotal
	if in.ToolSearch {
		mcpTokens = mcpLoaded
	}
	skillTokens := 0.0
	if skillIndex >= 0 {
		count, err := in.count(nil, []protocol.JSONObject{in.Schemas[skillIndex]})
		if err != nil {
			return Data{}, err
		}
		if count > 0 {
			data.Commands = &Commands{in.CommandTotal, in.CommandIncluded, count}
		}
		skills := Skills{Total: len(in.SkillMetadata), Included: len(in.SkillMetadata), Entries: []Skill{}}
		for _, meta := range in.SkillMetadata {
			words := []json.RawMessage{}
			for _, key := range []string{"name", "description", "whenToUse"} {
				value := meta.Get(key)
				if len(value) > 0 && string(value) != `""` && string(value) != "null" {
					words = append(words, value)
				}
			}
			joined, err := protocol.JoinJavaScriptValuesJSON(words, " ")
			if err != nil {
				return Data{}, err
			}
			count, err := rough(joined)
			if err != nil {
				return Data{}, err
			}
			name := meta.String("name")
			if meta.Get("userFacingName") != nil {
				name = meta.String("userFacingName")
			}
			source := meta.String("source")
			if meta.String("type") != "prompt" {
				source = "plugin"
			}
			skills.Entries = append(skills.Entries, Skill{name, source, count})
			skillTokens += count
		}
		if skillTokens > 0 {
			skills.Tokens = skillTokens
			data.Skills = &skills
		}
	}
	breakdown, err := in.messageBreakdown()
	if err != nil {
		return Data{}, err
	}
	data.Breakdown = breakdown
	apiMessages := []protocol.JSONObject{}
	for _, message := range in.Normalized {
		apiMessages = append(apiMessages, object(message.Get("message")))
	}
	messageTokens, err := in.count(apiMessages, nil)
	if err != nil {
		return Data{}, err
	}
	add := func(name string, tokens float64, color string, isDeferred bool) {
		if tokens > 0 {
			data.Categories = append(data.Categories, Category{name, tokens, color, isDeferred})
		}
	}
	add("System prompt", systemTokens, "promptBorder", false)
	systemName := "System tools"
	if in.Internal {
		systemName = "[ANT-ONLY] System tools"
	}
	add(systemName, builtinTokens-skillTokens, "inactive", false)
	add("MCP tools", mcpTokens, "cyan_FOR_SUBAGENTS_ONLY", false)
	add("MCP tools (deferred)", mcpDeferred, "inactive", true)
	add("System tools (deferred)", deferredBuiltinTokens, "inactive", true)
	add("Custom agents", agentTokens, "permission", false)
	add("Memory files", memoryTokens, "claude", false)
	add("Skills", skillTokens, "warning", false)
	add("Messages", messageTokens, "purple_FOR_SUBAGENTS_ONLY", false)
	estimated := 0.0
	for _, cat := range data.Categories {
		if !cat.Deferred {
			estimated += cat.Tokens
		}
	}
	data.APIUsage, err = preflightcore.CurrentContextUsage(in.History)
	if err != nil {
		return Data{}, err
	}
	total := math.Min(estimated, in.Window)
	if data.APIUsage != nil {
		usage := 0.0
		for _, key := range []string{"input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
			var n float64
			_ = json.Unmarshal(data.APIUsage.Get(key), &n)
			usage += n
		}
		ignore := in.LowTrust && hasMedia(in.History) && estimated > 0 && usage >= in.Window && estimated < in.Window && usage > math.Max(estimated*4, estimated+50000)
		if !ignore {
			canonical, err := in.EstimateCanonical(in.History)
			if err != nil {
				return Data{}, err
			}
			total = math.Min(math.Max(math.Max(estimated, usage), canonical), in.Window)
		}
	}
	data.Total = total
	data.Percentage = round(total / in.Window * 100)
	fixed := 0.0
	messagesAt := -1
	for i, cat := range data.Categories {
		if cat.Name == "Messages" {
			messagesAt = i
		} else if !cat.Deferred {
			fixed += cat.Tokens
		}
	}
	if messagesAt >= 0 {
		data.Categories[messagesAt].Tokens = math.Max(0, total-fixed)
	} else {
		add("Messages", math.Max(0, total-fixed), "purple_FOR_SUBAGENTS_ONLY", false)
	}
	actual := 0.0
	for _, cat := range data.Categories {
		if !cat.Deferred {
			actual += cat.Tokens
		}
	}
	reserved := 0.0
	if in.AutoCompact {
		threshold := in.EffectiveWindow - 13000
		data.AutoThreshold = &threshold
		reserved = math.Min(in.Window-threshold, math.Max(0, in.Window-actual))
		data.Categories = append(data.Categories, Category{"Autocompact buffer", reserved, "inactive", false})
	} else {
		reserved = math.Min(3000, math.Max(0, in.Window-actual))
		data.Categories = append(data.Categories, Category{"Compact buffer", reserved, "inactive", false})
	}
	data.Categories = append(data.Categories, Category{"Free space", math.Max(0, in.Window-actual-reserved), "promptBorder", false})
	data.Grid = grid(data.Categories, in.Window, in.TerminalWidth)
	if in.Internal {
		data.Deferred = &deferredDetails
		data.Sections = &sections
		details := []Detail{}
		data.SystemTools = &details
	}
	return data, nil
}

func hasMedia(history []protocol.JSONObject) bool {
	for _, message := range history {
		if message.String("type") == "attachment" {
			attachment := object(message.Get("attachment"))
			kind := object(attachment.Get("content")).String("type")
			if attachment.String("type") == "file" && (kind == "image" || kind == "pdf") {
				return true
			}
		}
		if message.String("type") != "user" && message.String("type") != "assistant" {
			continue
		}
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(object(message.Get("message")).Get("content"), &blocks)
		for _, block := range blocks {
			if block.String("type") == "image" || block.String("type") == "document" {
				return true
			}
		}
	}
	return false
}
