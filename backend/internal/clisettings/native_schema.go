package clisettings

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const NativeProcessProfile = "cli-settings-native-process-permission-rules-v1"

// NativeProcessProfile is a separate schema domain. The existing finite
// profile and its original fixtures retain their original rule boundaries.
func (s *Service) checkCapabilities(input protocol.JSONObject, fallback bool) error {
	if s.options.Profile != NativeProcessProfile {
		return checkCapabilities(input, fallback)
	}
	stripped, _, e := nativeRules(input)
	if e != nil {
		return e
	}
	return checkCapabilities(stripped, fallback)
}
func (s *Service) ValidateJSONContent(content, file string) (*ParsedSettings, error) {
	if s.options.Profile != NativeProcessProfile {
		return ValidateJSONContent(content, file)
	}
	return ValidateNativeJSONContent(content, file)
}

func nativeRules(input protocol.JSONObject) (protocol.JSONObject, *protocol.JSONObject, error) {
	stripped := input.Clone()
	stripped.Delete("hooks")
	for _, key := range nativeLoginKeys {
		stripped.Delete(key)
	}
	for _, key := range nativeHTTPHookKeys {
		stripped.Delete(key)
	}
	for _, key := range nativePluginKeys {
		stripped.Delete(key)
	}
	for _, key := range nativeMCPKeys {
		stripped.Delete(key)
	}
	for _, key := range nativeConsumerKeys {
		stripped.Delete(key)
	}
	for _, key := range []string{"attribution", "includeCoAuthoredBy", "apiKeyHelper", "awsCredentialExport", "awsAuthRefresh", "gcpAuthRefresh", "defaultShell", "outputStyle", "respectGitignore", "includeGitInstructions", "availableModels", "modelOverrides", "cleanupPeriodDays", "disableAgentSkillsDirectory", "autoUpdatesChannel", "minimumVersion", "autoMemoryEnabled", "autoMemoryDirectory", "webSearch", "skipWebFetchPreflight", "sandbox", "worktree", "fastMode", "fastModePerSessionOptIn", "maxConcurrentSubagents"} {
		stripped.Delete(key)
	}
	if language := input.Get("language"); language != nil {
		if receivedType(language) != "string" {
			return protocol.JSONObject{}, nil, &CapabilityError{"native language string"}
		}
		stripped.Delete("language")
	}
	if thinking := input.Get("alwaysThinkingEnabled"); thinking != nil {
		if receivedType(thinking) != "boolean" {
			return protocol.JSONObject{}, nil, &CapabilityError{"native alwaysThinkingEnabled boolean"}
		}
		stripped.Delete("alwaysThinkingEnabled")
	}
	if teams := input.Get("agentTeamsEnabled"); teams != nil {
		stripped.Delete("agentTeamsEnabled")
	}
	if input.Get("permissions") == nil || receivedType(input.Get("permissions")) != "object" {
		return stripped, nil, nil
	}
	var permissions protocol.JSONObject
	if e := permissions.UnmarshalJSON(input.Get("permissions")); e != nil {
		return protocol.JSONObject{}, nil, e
	}
	rules := protocol.NewJSONObject()
	for _, key := range []string{"allow", "deny", "ask"} {
		value := permissions.Get(key)
		if value == nil {
			continue
		}
		if receivedType(value) != "array" {
			return protocol.JSONObject{}, nil, &CapabilityError{"native permission array validation " + key}
		}
		var entries []json.RawMessage
		_ = json.Unmarshal(value, &entries)
		for _, entry := range entries {
			units, e := protocol.DecodeJavaScriptJSONString(entry)
			if e != nil {
				return protocol.JSONObject{}, nil, &CapabilityError{"native invalid permission-rule filtering/warning " + key}
			}
			for i, u := range units {
				if u >= 0xd800 && u <= 0xdbff && (i+1 == len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff) || u >= 0xdc00 && u <= 0xdfff && (i == 0 || units[i-1] < 0xd800 || units[i-1] > 0xdbff) {
					if !nativeLoneSkillRule(units) {
						return protocol.JSONObject{}, nil, &CapabilityError{"native unpaired UTF-16 permission rule"}
					}
				}
			}
			if !validNativeRule(string(utf16.Decode(units))) {
				return protocol.JSONObject{}, nil, &CapabilityError{"native invalid permission-rule filtering/warning " + key}
			}
		}
		rules.Set(key, value)
		permissions.Delete(key)
	}
	value, _ := permissions.MarshalJSON()
	stripped.Set("permissions", value)
	return stripped, &rules, nil
}

// Only Skill's logical command-name contents have an actual raw UTF-16
// consumer. Every other tool and invalid Skill syntax retains the old guard.
func nativeLoneSkillRule(units []uint16) bool {
	prefix := []uint16{'S', 'k', 'i', 'l', 'l', '('}
	if len(units) <= len(prefix) || units[len(units)-1] != ')' {
		return false
	}
	for i, u := range prefix {
		if units[i] != u {
			return false
		}
	}
	return validNativeRule(string(utf16.Decode(units)))
}

func ValidateNativeJSONContent(content, file string) (*ParsedSettings, error) {
	raw := parseJSONContent(content)
	if receivedType(raw) != "object" {
		return ValidateJSONContent(content, file)
	}
	var input protocol.JSONObject
	_ = input.UnmarshalJSON(raw)
	stripped, rules, e := nativeRules(input)
	if e != nil {
		return nil, e
	}
	encoded, _ := stripped.MarshalJSON()
	result, e := ValidateJSONContent(string(encoded), file)
	if e != nil {
		return result, e
	}
	deprecatedFields, deprecatedErrors := parseNativeDeprecatedAttribution(input, file)
	result.Errors = append(result.Errors, deprecatedErrors...)
	pluginFields, pluginErrors, e := parseNativePluginFields(input, file)
	if e != nil {
		return nil, e
	}
	result.Errors = append(result.Errors, pluginErrors...)
	mcpFields, mcpErrors := parseNativeMCPFields(input, file)
	result.Errors = append(result.Errors, mcpErrors...)
	consumerFields, consumerErrors := parseNativeConsumerSettings(input, file)
	result.Errors = append(result.Errors, consumerErrors...)
	for _, property := range consumerFields.OwnProperties() {
		_ = mcpFields.SetPropertyJSON(property.NameJSON, property.Value)
	}
	hooks, hookErrors, e := parseNativeHooks(input.Get("hooks"), file)
	if e != nil {
		return nil, e
	}
	result.Errors = append(result.Errors, hookErrors...)
	result.Errors = append(result.Errors, nativeHTTPHookErrors(input, file)...)
	result.Errors = append(result.Errors, nativeLoginErrors(input, file)...)
	result.Errors = append(result.Errors, nativeCleanupErrors(input.Get("cleanupPeriodDays"), file)...)
	result.Errors = append(result.Errors, nativeSubagentLimitErrors(input.Get("maxConcurrentSubagents"), file)...)
	result.Errors = append(result.Errors, nativeTerminalShellErrors(input.Get("defaultShell"), file)...)
	result.Errors = append(result.Errors, nativeInstallationChannelErrors(input.Get("autoUpdatesChannel"), file)...)
	result.Errors = append(result.Errors, nativeInstallationMinimumErrors(input.Get("minimumVersion"), file)...)
	sandbox, sandboxErrors := parseNativeSandbox(input.Get("sandbox"), file)
	result.Errors = append(result.Errors, sandboxErrors...)
	worktree, worktreeErrors := parseNativeWorktree(input.Get("worktree"), file)
	result.Errors = append(result.Errors, worktreeErrors...)
	webSearch, webErrors := parseNativeWebSearch(input.Get("webSearch"), file)
	result.Errors = append(result.Errors, webErrors...)
	for _, setting := range []struct{ name, expected string }{{"agentTeamsEnabled", "boolean"}, {"skipWebFetchPreflight", "boolean"}, {"respectGitignore", "boolean"}, {"includeGitInstructions", "boolean"}, {"includeCoAuthoredBy", "boolean"}, {"disableAgentSkillsDirectory", "boolean"}, {"autoMemoryEnabled", "boolean"}, {"autoMemoryDirectory", "string"}, {"outputStyle", "string"}, {"apiKeyHelper", "string"}, {"awsCredentialExport", "string"}, {"awsAuthRefresh", "string"}, {"gcpAuthRefresh", "string"}, {"fastMode", "boolean"}, {"fastModePerSessionOptIn", "boolean"}} {
		if value := input.Get(setting.name); value != nil && receivedType(value) != setting.expected {
			result.Errors = append(result.Errors, validationError(file, setting.name, setting.expected, receivedType(value)))
		}
	}
	modelFields := protocol.NewJSONObject()
	if value := input.Get("availableModels"); value != nil {
		if receivedType(value) != "array" {
			result.Errors = append(result.Errors, validationError(file, "availableModels", "array", receivedType(value)))
		} else {
			var models []json.RawMessage
			_ = json.Unmarshal(value, &models)
			for i, model := range models {
				if receivedType(model) != "string" {
					result.Errors = append(result.Errors, validationError(file, "availableModels."+strconv.Itoa(i), "string", receivedType(model)))
				}
			}
			modelFields.Set("availableModels", value)
		}
	}
	if value := input.Get("modelOverrides"); value != nil {
		if receivedType(value) != "object" {
			result.Errors = append(result.Errors, validationError(file, "modelOverrides", "record", receivedType(value)))
		} else {
			var overrides protocol.JSONObject
			_ = overrides.UnmarshalJSON(value)
			// The record schema ignores the prototype setter but preserves exact
			// UTF-16 identities of every other provider mapping key.
			overrides.Delete("__proto__")
			for _, property := range overrides.OwnProperties() {
				if receivedType(property.Value) != "string" {
					units, _ := protocol.DecodeJavaScriptJSONString(property.NameJSON)
					prefix, _ := protocol.DecodeJavaScriptJSONString(quoted("modelOverrides."))
					var diagnostic protocol.JSONObject
					_ = diagnostic.UnmarshalJSON(validationError(file, "modelOverrides.placeholder", "string", receivedType(property.Value)))
					diagnostic.Set("path", protocol.QuoteJavaScriptUTF16(append(prefix, units...)))
					encoded, _ := diagnostic.MarshalJSON()
					result.Errors = append(result.Errors, encoded)
				}
			}
			encoded, _ := overrides.MarshalJSON()
			modelFields.Set("modelOverrides", encoded)
		}
	}
	if len(result.Errors) != 0 {
		index := func(value json.RawMessage) int {
			var issue protocol.JSONObject
			_ = issue.UnmarshalJSON(value)
			name := strings.SplitN(issue.String("path"), ".", 2)[0]
			for i, key := range activeKeys {
				if key == name {
					return i
				}
			}
			return len(activeKeys)
		}
		sort.SliceStable(result.Errors, func(i, j int) bool { return index(result.Errors[i]) < index(result.Errors[j]) })
		result.Settings = nil
		return result, nil
	}
	if result.Settings == nil {
		return result, nil
	}
	// Source PermissionsSchema puts known rule arrays before mode and raw
	// passthrough fields. Original JSON strings are retained without decoding.
	if rules != nil {
		permissions := rules.Clone()
		var remaining protocol.JSONObject
		_ = remaining.UnmarshalJSON(result.Settings.Get("permissions"))
		for _, p := range remaining.OwnProperties() {
			_ = permissions.SetPropertyJSON(p.NameJSON, p.Value)
		}
		encoded, _ = permissions.MarshalJSON()
		result.Settings.Set("permissions", encoded)
	}
	for _, nativeKey := range append(append(append([]string{"attribution", "includeCoAuthoredBy", "apiKeyHelper", "awsCredentialExport", "awsAuthRefresh", "gcpAuthRefresh", "defaultShell", "respectGitignore", "includeGitInstructions", "cleanupPeriodDays", "availableModels", "modelOverrides", "maxConcurrentSubagents", "agentTeamsEnabled", "disableAgentSkillsDirectory", "outputStyle", "autoUpdatesChannel", "minimumVersion", "autoMemoryEnabled", "autoMemoryDirectory", "language", "alwaysThinkingEnabled", "webSearch", "skipWebFetchPreflight", "sandbox", "worktree", "hooks", "fastMode", "fastModePerSessionOptIn", "maxConcurrentSubagents"}, nativePluginKeys...), append(append([]string{}, nativeMCPKeys...), nativeConsumerKeys...)...), append(append([]string{}, nativeHTTPHookKeys...), nativeLoginKeys...)...) {
		if input.Get(nativeKey) == nil {
			continue
		}
		out := protocol.NewJSONObject()
		inserted := false
		teamsIndex := 0
		for i, key := range activeKeys {
			if key == nativeKey {
				teamsIndex = i
				break
			}
		}
		for _, property := range result.Settings.OwnProperties() {
			index := len(activeKeys)
			for i, key := range activeKeys {
				if string(property.NameJSON) == string(quoted(key)) {
					index = i
					break
				}
			}
			if !inserted && index > teamsIndex {
				value := input.Get(nativeKey)
				if deprecatedFields.Get(nativeKey) != nil {
					value = deprecatedFields.Get(nativeKey)
				}
				if modelFields.Get(nativeKey) != nil {
					value = modelFields.Get(nativeKey)
				}
				if pluginFields != nil && pluginFields.Get(nativeKey) != nil {
					value = pluginFields.Get(nativeKey)
				}
				if mcpFields.Get(nativeKey) != nil {
					value = mcpFields.Get(nativeKey)
				}
				if nativeKey == "sandbox" {
					value = sandbox
				}
				if nativeKey == "worktree" {
					value = worktree
				}
				if nativeKey == "hooks" {
					value = hooks
				}
				if nativeKey == "webSearch" {
					value = webSearch
				}
				out.Set(nativeKey, value)
				inserted = true
			}
			_ = out.SetPropertyJSON(property.NameJSON, property.Value)
		}
		if !inserted {
			value := input.Get(nativeKey)
			if deprecatedFields.Get(nativeKey) != nil {
				value = deprecatedFields.Get(nativeKey)
			}
			if modelFields.Get(nativeKey) != nil {
				value = modelFields.Get(nativeKey)
			}
			if pluginFields != nil && pluginFields.Get(nativeKey) != nil {
				value = pluginFields.Get(nativeKey)
			}
			if mcpFields.Get(nativeKey) != nil {
				value = mcpFields.Get(nativeKey)
			}
			if nativeKey == "sandbox" {
				value = sandbox
			}
			if nativeKey == "worktree" {
				value = worktree
			}
			if nativeKey == "hooks" {
				value = hooks
			}
			if nativeKey == "webSearch" {
				value = webSearch
			}
			out.Set(nativeKey, value)
		}
		result.Settings = &out
	}
	return result, nil
}

// IsNativeCleanupPeriodDays admits exactly Source's optional nonnegative
// integer setting. The production persistence and cleanup services consume
// it; finite settings domains retain their numeric-zero boundary.
func IsNativeCleanupPeriodDays(value json.RawMessage) bool {
	return value == nil || len(nativeCleanupErrors(value, "")) == 0
}

func nativeCleanupErrors(value json.RawMessage, file string) []json.RawMessage {
	if value == nil {
		return nil
	}
	received := receivedType(value)
	if received != "number" {
		return []json.RawMessage{validationError(file, "cleanupPeriodDays", "number", received)}
	}
	n, err := strconv.ParseFloat(string(value), 64)
	if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
		return []json.RawMessage{validationError(file, "cleanupPeriodDays", "number", "number")}
	}
	errors := []json.RawMessage{}
	minimum := func(bound string, tip bool) json.RawMessage {
		out := protocol.NewJSONObject(protocol.JSONField{Name: "file", Value: quoted(file)}, protocol.JSONField{Name: "path", Value: quoted("cleanupPeriodDays")}, protocol.JSONField{Name: "message", Value: quoted("Number must be greater than or equal to " + bound)}, protocol.JSONField{Name: "expected", Value: quoted(bound)})
		if tip {
			out.Set("suggestion", quoted("Must be 0 or greater. Set a positive number for days to retain transcripts (default is 365). Setting 0 disables session persistence entirely: no transcripts are written and existing transcripts are deleted."))
		}
		encoded, _ := out.MarshalJSON()
		return encoded
	}
	if n < 0 {
		errors = append(errors, minimum("0", true))
	}
	if math.Trunc(n) != n {
		errors = append(errors, validationError(file, "cleanupPeriodDays", "int", "number"))
	} else if n < -9007199254740991 {
		errors = append(errors, minimum("-9007199254740991", false))
	} else if n > 9007199254740991 {
		out := protocol.NewJSONObject(protocol.JSONField{Name: "file", Value: quoted(file)}, protocol.JSONField{Name: "path", Value: quoted("cleanupPeriodDays")}, protocol.JSONField{Name: "message", Value: quoted("Too big: expected int to be <=9007199254740991")})
		encoded, _ := out.MarshalJSON()
		errors = append(errors, encoded)
	}
	return errors
}

func unescaped(s string, i int) bool {
	count := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		count++
	}
	return count%2 == 0
}
func validNativeRule(rule string) bool {
	if trimJS(rule) == "" {
		return false
	}
	open, close, first, last := 0, 0, -1, -1
	for i := 0; i < len(rule); i++ {
		if !unescaped(rule, i) {
			continue
		}
		switch rule[i] {
		case '(':
			open++
			if first < 0 {
				first = i
			}
			if i+1 < len(rule) && rule[i+1] == ')' {
				return false
			}
		case ')':
			close++
			last = i
		}
	}
	if open != close {
		return false
	}
	name, content := rule, ""
	if first >= 0 && last == len(rule)-1 && last > first && first > 0 {
		name, content = rule[:first], rule[first+1:last]
		content = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(content, `\(`, "("), `\)`, ")"), `\\`, `\`)
		if content == "*" {
			content = ""
		}
	}
	switch name {
	case "Task":
		name = "Agent"
	case "KillShell":
		name = "TaskStop"
	case "AgentOutputTool", "BashOutputTool":
		name = "TaskOutput"
	}
	parts := strings.Split(name, "__")
	if len(parts) >= 2 && parts[0] == "mcp" && parts[1] != "" {
		return open == 0
	}
	if name == "" {
		return false
	}
	firstRune := []rune(name)[0]
	if unicode.ToUpper(firstRune) != firstRune {
		return false
	}
	if name == "WebSearch" && strings.ContainsAny(content, "*?") {
		return false
	}
	if name == "WebFetch" && content != "" && (strings.Contains(content, "://") || strings.HasPrefix(content, "http") || !strings.HasPrefix(content, "domain:")) {
		return false
	}
	if name == "Bash" && content != "" && (strings.Contains(content, ":*") && !strings.HasSuffix(content, ":*") || content == ":*") {
		return false
	}
	switch name {
	case "Read", "Write", "Edit", "Glob", "NotebookRead", "NotebookEdit":
		if strings.Contains(content, ":*") {
			return false
		}
		if strings.Contains(content, "*") && !regexp.MustCompile(`^\*|\*$|\*\*|/\*|\*\.|\*\)`).MatchString(content) && !strings.Contains(content, "**") {
			return false
		}
	}
	return true
}
