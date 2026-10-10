package clibootstrap

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	weburl "github.com/nlnwa/whatwg-url/url"
)

const ultracodeMenuDescription = "xhigh effort + dynamic workflows for maximum thoroughness"
const effortHelp = "Usage: /effort [low|medium|high|xhigh|max|ultracode|auto]\n\nEffort levels:\n- low: Quick, straightforward implementation\n- medium: Balanced approach with standard testing\n- high: Comprehensive implementation with extensive testing\n- xhigh: Extra-high reasoning for especially complex tasks\n- max: Maximum capability with deepest reasoning\n- ultracode: xhigh effort + dynamic workflows for maximum thoroughness (this session only)\n- auto: Use the default effort level for your model"

var numericEffort = regexp.MustCompile(`^-?\d+$`)

func effortDescription(level string) string {
	switch level {
	case "low":
		return "Quick, straightforward implementation with minimal overhead"
	case "medium":
		return "Balanced approach with standard implementation and testing"
	case "high":
		return "Comprehensive implementation with extensive testing and documentation"
	case "xhigh":
		return "Extra-high reasoning for especially complex tasks"
	case "max":
		return "Maximum capability with deepest reasoning"
	}
	return "Balanced approach with standard implementation and testing"
}
func parsedEffortEnv(env map[string]string) (value json.RawMessage, present, auto bool) {
	raw := env["CLAUDE_CODE_EFFORT_LEVEL"]
	if strings.EqualFold(raw, "auto") || strings.EqualFold(raw, "unset") {
		return nil, true, true
	}
	normalized := strings.ToLower(protocol.TrimJavaScriptString(raw))
	switch normalized {
	case "low", "medium", "high", "xhigh", "max":
		return quoteMain(normalized), true, false
	}
	if numericEffort.MatchString(normalized) {
		if number, e := strconv.ParseFloat(normalized, 64); e == nil && !math.IsInf(number, 0) && math.Trunc(number) == number {
			return rawMain(number), true, false
		}
	}
	return nil, false, false
}
func sourceXHighEffort(model string, env map[string]string) bool {
	thirdParty := false
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		thirdParty = thirdParty || nativeEnvTruthy(env[key])
	}
	if base := env["ANTHROPIC_BASE_URL"]; base != "" {
		u, e := weburl.Parse(base)
		thirdParty = thirdParty || e != nil || u.Host() != "api.anthropic.com" && !(env["USER_TYPE"] == "ant" && u.Host() == "api-staging.anthropic.com")
	}
	if thirdParty {
		if result, present := requestcontext.NativeModelCapabilityOverride(model, env, "xhigh_effort"); present {
			return result
		}
	}
	if providerproxy.IsOpenAIResponsesModel(model) {
		entry := providerproxy.OpenAIModelCatalogEntry(model, providerproxy.OpenAIModelCatalog())
		if entry == nil {
			return true
		}
		for _, level := range entry.SupportedReasoningEfforts {
			if level == "xhigh" {
				return true
			}
		}
		return false
	}
	// Source effort.getGrokCatalogEntry performs an exact catalog lookup.
	// Grok transport aliases (including claude-* -> default Grok) must not
	// change the capability of the model selected in the effort command.
	key := strings.ToLower(protocol.TrimJavaScriptString(model))
	for _, entry := range providerproxy.GrokModelCatalog() {
		if entry.Value == key {
			for _, level := range entry.ReasoningEfforts {
				if level == "xhigh" {
					return true
				}
			}
			return false
		}
	}
	if thirdParty {
		return false
	}
	model = strings.ToLower(model)
	for _, name := range []string{"opus-4-7", "opus-5", "opus-4-8", "sonnet-5", "fable-5", "mythos-5"} {
		if strings.Contains(model, name) {
			return true
		}
	}
	return false
}
func (m *MainPrint) nativeEffortCommand(scope *cooperative.Scope, args string) (string, error) {
	if !scope.BelongsTo(m.Owner) {
		return "", cooperative.ErrRestricted
	}
	if !m.options.Interactive {
		return "", mainCap("interactive /effort AppState producer")
	}
	args = protocol.TrimJavaScriptString(args)
	env := m.nativeOpenAIEnvironment()
	s, e := m.ultracodeState(scope)
	if e != nil {
		return "", e
	}
	model, e := m.Host.InferenceModel(scope)
	if e != nil {
		return "", e
	}
	switch args {
	case "help", "-h", "--help":
		return effortHelp, nil
	case "", "current", "status":
		value, _, e := m.Host.InferenceEffort(scope)
		if e != nil {
			return "", e
		}
		override, present, auto := parsedEffortEnv(env)
		if present {
			value = override
		}
		if value == nil {
			level := "high"
			if !auto {
				_, _, level = requestcontext.NativeModelPickerEffort(model, env)
			}
			return "Effort level: auto (currently " + level + ")", nil
		}
		var level string
		if json.Unmarshal(value, &level) != nil {
			level = string(value)
		}
		return "Current effort level: " + level + " (" + effortDescription(level) + ")", nil
	}
	normalized := strings.ToLower(args)
	value := quoteMain(normalized)
	ultra := normalized == "ultracode"
	persisted := false
	if ultra {
		enabled, e := m.workflowDefinitions.EnabledOnOwner(scope)
		if e != nil {
			return "", e
		}
		if !enabled {
			return "Ultracode needs dynamic workflows enabled (see /config). Valid options are: low, medium, high, xhigh, max, ultracode, auto", nil
		}
		if !sourceXHighEffort(model, env) {
			return "Ultracode runs at xhigh effort, which " + model + " doesn't support — switch to an xhigh-capable model. Valid options are: low, medium, high, xhigh, max, ultracode, auto", nil
		}
		value = quoteMain("xhigh")
	} else if normalized == "auto" || normalized == "unset" {
		value = nil
		if e = m.writePickerSetting(clisettings.Patch{Object: protocol.NewJSONObject(), UndefinedPaths: [][]json.RawMessage{{quoteMain("effortLevel")}}}); e != nil {
			return "Failed to set effort level: " + e.Error(), nil
		}
	} else {
		switch normalized {
		case "low", "medium", "high", "xhigh", "max":
		default:
			return "Invalid argument: " + args + ". Valid options are: low, medium, high, max, auto", nil
		}
		persisted = normalized == "low" || normalized == "medium" || normalized == "high" || normalized == "max" && env["USER_TYPE"] == "ant"
		if persisted {
			if e = m.writePickerSetting(clisettings.Patch{Object: protocol.NewJSONObject(protocol.JSONField{Name: "effortLevel", Value: value})}); e != nil {
				return "Failed to set effort level: " + e.Error(), nil
			}
		}
	}
	if persisted || value == nil {
		if e = m.Host.AcceptInternalModelSettings(scope); e != nil {
			return "", e
		}
	}
	if e = m.Host.SetNativeTerminalEffort(scope, value); e != nil {
		return "", e
	}
	s.active = ultra
	if report := m.nativeFileReporter(); report != nil {
		name := normalized
		if normalized == "unset" {
			name = "auto"
		}
		if e = report(scope, analytics.Event{Name: "tengu_effort_command", Metadata: protocol.NewJSONObject(protocol.JSONField{Name: "effort", Value: quoteMain(name)})}); e != nil {
			return "", e
		}
	}
	override, present, auto := parsedEffortEnv(env)
	conflict := present && string(override) != string(value)
	if ultra {
		if present && !auto && string(override) != `"xhigh"` {
			return "CLAUDE_CODE_EFFORT_LEVEL=" + env["CLAUDE_CODE_EFFORT_LEVEL"] + " overrides effort this session — clear it and ultracode takes over", nil
		}
		return "Set effort level to ultracode (this session only): " + ultracodeMenuDescription, nil
	}
	if value == nil {
		if present && !auto {
			return "Cleared effort from settings, but CLAUDE_CODE_EFFORT_LEVEL=" + env["CLAUDE_CODE_EFFORT_LEVEL"] + " still controls this session", nil
		}
		return "Effort level set to auto", nil
	}
	if conflict {
		if !persisted {
			return "Not applied: CLAUDE_CODE_EFFORT_LEVEL=" + env["CLAUDE_CODE_EFFORT_LEVEL"] + " overrides effort this session, and " + normalized + " is session-only (nothing saved)", nil
		}
		return "CLAUDE_CODE_EFFORT_LEVEL=" + env["CLAUDE_CODE_EFFORT_LEVEL"] + " overrides this session — clear it and " + normalized + " takes over", nil
	}
	suffix := ""
	if !persisted {
		suffix = " (this session only)"
	}
	return "Set effort level to " + normalized + suffix + ": " + effortDescription(normalized), nil
}
