package installationdiagnostics

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/preflightcore"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

type ContextWarning struct {
	Type         string   `json:"type"`
	Severity     string   `json:"severity"`
	Message      string   `json:"message"`
	Details      []string `json:"details"`
	CurrentValue float64  `json:"currentValue"`
	Threshold    float64  `json:"threshold"`
}
type ContextWarnings struct {
	Memory *ContextWarning `json:"claudeMdWarning"`
	Agents *ContextWarning `json:"agentWarning"`
	MCP    *ContextWarning `json:"mcpWarning"`
	Rules  *ContextWarning `json:"unreachableRulesWarning"`
}
type MCPTool struct {
	Name, Description string
	Schema            protocol.JSONObject
	AlwaysLoad        bool
}
type ContextInput struct {
	Memory                     []usercontext.MemoryFile
	Agents                     []agentcatalog.Definition
	Permission                 protocol.JSONObject
	SandboxAutoAllow, Deferred bool
	MCP                        []MCPTool
	Count                      func([]protocol.JSONObject) (*float64, error)
}

func quote(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func rough(value string) float64         { n, _ := preflightcore.RoughContent(quote(value)); return n }
func locale(value float64) string {
	text := strconv.FormatFloat(value, 'f', -1, 64)
	parts := strings.Split(text, ".")
	whole := parts[0]
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	parts[0] = whole
	return strings.Join(parts, ".")
}
func Context(in ContextInput) ContextWarnings {
	result := ContextWarnings{}
	large := []usercontext.MemoryFile{}
	for _, file := range in.Memory {
		units, _ := protocol.DecodeJavaScriptJSONString(file.PromptContent())
		if len(units) > 40000 {
			large = append(large, file)
		}
	}
	sort.SliceStable(large, func(i, j int) bool {
		a, _ := protocol.DecodeJavaScriptJSONString(large[i].PromptContent())
		b, _ := protocol.DecodeJavaScriptJSONString(large[j].PromptContent())
		return len(a) > len(b)
	})
	if len(large) > 0 {
		details := []string{}
		for _, file := range large {
			units, _ := protocol.DecodeJavaScriptJSONString(file.PromptContent())
			details = append(details, file.Path+": "+locale(float64(len(units)))+" chars")
		}
		message := fmt.Sprintf("%d large CLAUDE.md files detected (each > 40,000 chars)", len(large))
		if len(large) == 1 {
			units, _ := protocol.DecodeJavaScriptJSONString(large[0].PromptContent())
			message = "Large CLAUDE.md file detected (" + locale(float64(len(units))) + " chars > 40,000)"
		}
		result.Memory = &ContextWarning{"claudemd_files", "warning", message, details, float64(len(large)), 40000}
	}
	type agentTokens struct {
		name   string
		tokens float64
	}
	agents := []agentTokens{}
	total := 0.0
	for _, agent := range in.Agents {
		if agent.Source == "built-in" {
			continue
		}
		tokens := rough(agent.Name + ": " + agent.Description)
		total += tokens
		agents = append(agents, agentTokens{agent.Name, tokens})
	}
	if total > 15000 {
		sort.SliceStable(agents, func(i, j int) bool { return agents[i].tokens > agents[j].tokens })
		details := []string{}
		for _, agent := range agents[:min(len(agents), 5)] {
			details = append(details, agent.name+": ~"+locale(agent.tokens)+" tokens")
		}
		if len(agents) > 5 {
			details = append(details, fmt.Sprintf("(%d more custom agents)", len(agents)-5))
		}
		result.Agents = &ContextWarning{"agent_descriptions", "warning", "Large agent descriptions (~" + locale(total) + " tokens > 15,000)", details, total, 15000}
	}
	if len(in.MCP) > 0 {
		schemas := []protocol.JSONObject{}
		for _, tool := range in.MCP {
			schemas = append(schemas, tool.Schema.Clone())
		}
		var count *float64
		var err error
		if in.Count != nil {
			count, err = in.Count(schemas)
		}
		if err != nil {
			estimated := 0.0
			for _, tool := range in.MCP {
				units, _ := protocol.DecodeJavaScriptJSONString(quote(tool.Name + tool.Description))
				estimated += rough(strconv.Itoa(len(units)))
			}
			if estimated > 25000 {
				result.MCP = &ContextWarning{"mcp_tools", "warning", "Large MCP tools context (~" + locale(estimated) + " tokens estimated > 25,000)", []string{fmt.Sprintf("%d MCP tools detected (token count estimated)", len(in.MCP))}, estimated, 25000}
			}
		} else {
			rawCount := 0.0
			if count != nil {
				rawCount = *count
			}
			tokens := math.Max(0, rawCount-500)
			estimates := []float64{}
			sum := 0.0
			for _, tool := range in.MCP {
				schema := protocol.NewJSONObject()
				schema.Set("name", quote(tool.Name))
				schema.Set("description", quote(tool.Description))
				input := tool.Schema.Get("input_schema")
				if input == nil {
					input = []byte(`{}`)
				}
				schema.Set("input_schema", input)
				raw, _ := schema.MarshalJSON()
				estimate := rough(string(protocol.StringifyValidatedJavaScriptJSON(raw)))
				estimates = append(estimates, estimate)
				sum += estimate
			}
			if sum == 0 {
				sum = 1
			}
			type group struct {
				name   string
				count  int
				tokens float64
			}
			groups := []group{}
			indices := map[string]int{}
			loaded := 0.0
			for i, tool := range in.MCP {
				n := math.Floor(estimates[i]/sum*tokens + 0.5)
				if !in.Deferred || tool.AlwaysLoad {
					loaded += n
				}
				parts := strings.Split(tool.Name, "__")
				server := "unknown"
				if len(parts) > 1 && parts[1] != "" {
					server = parts[1]
				}
				index, ok := indices[server]
				if !ok {
					index = len(groups)
					indices[server] = index
					groups = append(groups, group{name: server})
				}
				groups[index].count++
				groups[index].tokens += n
			}
			if in.Deferred {
				tokens = loaded
			}
			if tokens > 25000 {
				sort.SliceStable(groups, func(i, j int) bool { return groups[i].tokens > groups[j].tokens })
				details := []string{}
				for _, g := range groups[:min(len(groups), 5)] {
					details = append(details, fmt.Sprintf("%s: %d tools (~%s tokens)", g.name, g.count, locale(g.tokens)))
				}
				if len(groups) > 5 {
					details = append(details, fmt.Sprintf("(%d more servers)", len(groups)-5))
				}
				result.MCP = &ContextWarning{"mcp_tools", "warning", "Large MCP tools context (~" + locale(tokens) + " tokens > 25,000)", details, tokens, 25000}
			}
		}
	}
	result.Rules = unreachable(in.Permission, in.SandboxAutoAllow)
	return result
}

type permissionRule struct {
	value  protocol.JSONObject
	source string
}

var ruleSources = []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings", "cliArg", "command", "session"}

func rules(context protocol.JSONObject, key string) []permissionRule {
	object := protocol.NewJSONObject()
	_ = object.UnmarshalJSON(context.Get(key))
	result := []permissionRule{}
	for _, source := range ruleSources {
		var list []json.RawMessage
		_ = json.Unmarshal(object.Get(source), &list)
		for _, raw := range list {
			if parsed, e := sdkpermissions.ParsePermissionRuleValue(raw); e == nil {
				result = append(result, permissionRule{parsed, source})
			}
		}
	}
	return result
}
func sourceName(source string) string {
	return map[string]string{"userSettings": "user settings", "projectSettings": "shared project settings", "localSettings": "project local settings", "flagSettings": "command line arguments", "policySettings": "enterprise managed settings", "cliArg": "CLI argument", "command": "command configuration", "session": "current session"}[source]
}
func unreachable(context protocol.JSONObject, sandbox bool) *ContextWarning {
	ask, deny := rules(context, "alwaysAskRules"), rules(context, "alwaysDenyRules")
	details := []string{}
	n := 0
	for _, allow := range rules(context, "alwaysAllowRules") {
		if allow.value.Get("ruleContent") == nil {
			continue
		}
		tool := allow.value.String("toolName")
		var shadow *permissionRule
		behavior := "deny"
		for i := range deny {
			if deny[i].value.String("toolName") == tool && deny[i].value.Get("ruleContent") == nil {
				shadow = &deny[i]
				break
			}
		}
		if shadow == nil {
			behavior = "ask"
			for i := range ask {
				if ask[i].value.String("toolName") == tool && ask[i].value.Get("ruleContent") == nil {
					shadow = &ask[i]
					break
				}
			}
		}
		if shadow == nil {
			continue
		}
		if behavior == "ask" && tool == "Bash" && sandbox && shadow.source != "projectSettings" && shadow.source != "policySettings" && shadow.source != "command" {
			continue
		}
		n++
		verb := "Blocked"
		if behavior == "ask" {
			verb = "Shadowed"
		}
		content := allow.value.String("ruleContent")
		content = strings.ReplaceAll(content, `\`, `\\`)
		content = strings.ReplaceAll(content, "(", `\(`)
		content = strings.ReplaceAll(content, ")", `\)`)
		raw := tool + "(" + content + ")"
		details = append(details, raw+": "+verb+" by \""+tool+"\" "+behavior+" rule (from "+sourceName(shadow.source)+")", "  Fix: Remove the \""+tool+"\" "+behavior+" rule from "+sourceName(shadow.source)+", or remove the specific allow rule from "+sourceName(allow.source))
	}
	if n == 0 {
		return nil
	}
	plural := "unreachable permission rules"
	if n == 1 {
		plural = "unreachable permission rule"
	}
	return &ContextWarning{"unreachable_rules", "warning", fmt.Sprintf("%d %s detected", n, plural), details, float64(n), 0}
}
