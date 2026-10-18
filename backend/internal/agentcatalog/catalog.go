// Package agentcatalog owns native configured-agent discovery and listing
// projections. It never spawns an agent, calls a model, or writes persistence.
package agentcatalog

import (
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentbuiltinmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"math"
	"sort"
	"strconv"
	"strings"
)

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "Go agents capability is not migrated: " + e.Boundary
}
func cap(s string) error { return &CapabilityError{s} }

type Definition struct {
	Name, Description, Model, Memory, Source, FilePath, ContentHash, Body string
	BaseDir                                                               string
	Frontmatter                                                           protocol.JSONObject
	OverriddenBy                                                          string
	Effort                                                                json.RawMessage
	ModelSource, EffortSource                                             string
	// Fields is the actual parsed AgentDefinition data, excluding callbacks.
	// Listing, desktop editing, prompt descriptions and execution share it.
	Fields       protocol.JSONObject
	SystemPrompt string
	promptJSON   json.RawMessage
	PromptArity  int
}
type SourceGroup struct{ Label, Source string }

var groups = []SourceGroup{{"User agents", "userSettings"}, {"Project agents", "projectSettings"}, {"Local agents", "localSettings"}, {"Managed agents", "policySettings"}, {"Plugin agents", "plugin"}, {"CLI arg agents", "flagSettings"}, {"Built-in agents", "built-in"}}

func Groups() []SourceGroup { return append([]SourceGroup{}, groups...) }

// Active preserves Map insertion position while later source groups replace
// the value. Closest-cwd/path-first wins inside each source, not last-file wins.
func Active(all []Definition) []Definition {
	out := []Definition{}
	slots := map[string]int{}
	for _, source := range []string{"built-in", "plugin", "userSettings", "projectSettings", "flagSettings", "policySettings"} {
		seen := map[string]bool{}
		for _, d := range all {
			if d.Source != source || seen[d.Name] {
				continue
			}
			seen[d.Name] = true
			if i, ok := slots[d.Name]; ok {
				out[i] = d
			} else {
				slots[d.Name] = len(out)
				out = append(out, d)
			}
		}
	}
	return out
}
func Resolve(all, active []Definition) []Definition {
	winners := map[string]string{}
	for _, d := range active {
		winners[d.Name] = d.Source
	}
	seen := map[string]bool{}
	out := []Definition{}
	for _, d := range all {
		key := d.Name + ":" + d.Source
		if seen[key] {
			continue
		}
		seen[key] = true
		if source := winners[d.Name]; source != "" && source != d.Source {
			d.OverriddenBy = source
		}
		out = append(out, d)
	}
	return out
}
func sourceLabel(source string) string {
	switch source {
	case "userSettings":
		return "user"
	case "projectSettings":
		return "project"
	case "localSettings":
		return "local"
	case "policySettings":
		return "managed"
	case "plugin":
		return "plugin"
	case "flagSettings":
		return "flag"
	case "built-in":
		return "built-in"
	}
	return source
}
func Render(all []Definition) string {
	resolved := Resolve(all, Active(all))
	lines := []string{}
	total := 0
	comparator := collate.New(language.English, collate.IgnoreCase, collate.IgnoreDiacritics)
	for _, group := range groups {
		entries := []Definition{}
		for _, d := range resolved {
			if d.Source == group.Source {
				entries = append(entries, d)
			}
		}
		sort.SliceStable(entries, func(i, j int) bool { return comparator.CompareString(entries[i].Name, entries[j].Name) < 0 })
		if len(entries) == 0 {
			continue
		}
		lines = append(lines, group.Label+":")
		for _, d := range entries {
			model := d.Model
			if model == "" {
				model = "inherit"
			}
			parts := []string{d.Name, model}
			if d.Memory != "" {
				parts = append(parts, d.Memory+" memory")
			}
			prefix := "  "
			if d.OverriddenBy != "" {
				prefix += "(shadowed by " + sourceLabel(d.OverriddenBy) + ") "
			} else {
				total++
			}
			lines = append(lines, prefix+strings.Join(parts, " · "))
		}
		lines = append(lines, "")
	}
	if len(lines) == 0 {
		return "No agents found.\n"
	}
	return fmt.Sprintf("%d active agents\n\n%s\n", total, strings.TrimRight(strings.Join(lines, "\n"), " \t\r\n"))
}

func truthy(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	}
	return true
}
func boolEnv(s string) bool {
	switch strings.ToLower(trimJS(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func effort(raw json.RawMessage) bool { return effortValue(raw) != nil }
func effortValue(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return nil
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	if n, ok := value.(float64); ok && !math.IsInf(n, 0) && math.Trunc(n) == n {
		return protocol.StringifyValidatedJavaScriptJSON(raw)
	}
	coerced, e := protocol.JavaScriptStringJSON(raw)
	if e != nil {
		return nil
	}
	var text string
	_ = json.Unmarshal(coerced, &text)
	text = strings.ToLower(trimJS(text))
	for _, level := range []string{"low", "medium", "high", "xhigh", "max"} {
		if text == level {
			b, _ := json.Marshal(text)
			return b
		}
	}
	if text == "" || text == "-" {
		return nil
	}
	for i, r := range text {
		if !(r >= '0' && r <= '9') && !(i == 0 && r == '-') {
			return nil
		}
	}
	n, e := strconv.ParseFloat(text, 64)
	if e != nil || math.IsInf(n, 0) || math.Trunc(n) != n {
		return nil
	}
	b, _ := json.Marshal(n)
	return b
}

func (s *Service) builtin(scope *cooperative.Scope) ([]Definition, error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	env := s.options.Environment
	if env("USER_TYPE") == "ant" {
		return nil, cap("internal built-in feature/growthbook producer")
	}
	out := []Definition{}
	if !(s.options.NonInteractive && boolEnv(env("CLAUDE_AGENT_SDK_DISABLE_BUILTIN_AGENTS"))) {
		entry := env("CLAUDE_CODE_ENTRYPOINT")
		for _, builtin := range agentbuiltinmetadata.Stock() {
			fields := builtin.Fields
			if fields.String("agentType") == "claude-code-guide" && (entry == "sdk-ts" || entry == "sdk-py" || entry == "sdk-cli") {
				continue
			}
			out = append(out, Definition{Name: fields.String("agentType"), Description: fields.String("whenToUse"), Model: fields.String("model"), Source: "built-in", Fields: fields, SystemPrompt: builtin.SystemPrompt, PromptArity: builtin.PromptArity})
		}
	}
	locked, e := s.locked()
	if e != nil {
		return nil, e
	}
	for _, source := range s.options.EnabledSources {
		if locked && source != "policySettings" {
			continue
		}
		settings, e := s.options.Settings(source)
		if e != nil {
			return nil, e
		}
		if settings == nil {
			continue
		}
		var overrides protocol.JSONObject
		if overrides.UnmarshalJSON(settings.Get("builtInAgentOverrides")) != nil {
			continue
		}
		for i := range out {
			var entry protocol.JSONObject
			if entry.UnmarshalJSON(overrides.Get(out[i].Name)) != nil {
				continue
			}
			if raw := entry.Get("model"); truthy(raw) {
				model, e := exactString(raw)
				if e != nil {
					return nil, e
				}
				model = trimJS(model)
				if model != "" {
					out[i].Model = model
					out[i].ModelSource = source
					out[i].Fields.Set("model", encodeField(model))
				}
			}
			if raw := entry.Get("effort"); effort(raw) {
				out[i].Effort = effortValue(raw)
				out[i].EffortSource = source
				out[i].Fields.Set("effort", out[i].Effort)
			}
		}
	}
	return out, nil
}
