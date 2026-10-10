package clisettings

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

// Literal release-key recipe from SettingsSchema, not Source-rendered output.
// Inactive XAA/internal/VOICE/KAIROS keys are genuine passthrough in this profile.
var activeKeys = strings.Fields(`$schema apiKeyHelper awsCredentialExport awsAuthRefresh gcpAuthRefresh fileSuggestion respectGitignore cleanupPeriodDays env attribution includeCoAuthoredBy includeGitInstructions permissions model availableModels modelOverrides enableAllProjectMcpServers enabledMcpjsonServers disabledMcpjsonServers allowedMcpServers deniedMcpServers hooks worktree disableAllHooks disableWorkflows enableWorkflows agentTeamsEnabled workflowKeywordTriggerEnabled workflowSizeGuideline disableAgentSkillsDirectory defaultShell allowManagedHooksOnly allowedHttpHookUrls httpHookAllowedEnvVars allowManagedPermissionRulesOnly allowManagedMcpServersOnly strictPluginOnlyCustomization statusLine enabledPlugins extraKnownMarketplaces strictKnownMarketplaces blockedMarketplaces forceLoginMethod forceLoginOrgUUID otelHeadersHelper outputStyle language skipWebFetchPreflight webSearch feedbackSurveyRate sandbox spinnerTipsEnabled spinnerVerbs spinnerTipsOverride syntaxHighlightingDisabled terminalTitleFromRename alwaysThinkingEnabled effortLevel advisorModel fastMode fastModePerSessionOptIn promptSuggestionEnabled showClearContextOnPlanAccept agent builtInAgentOverrides companyAnnouncements pluginConfigs remote autoUpdatesChannel minimumVersion plansDirectory channelsEnabled allowedChannelPlugins prefersReducedMotion autoMemoryEnabled autoMemoryDirectory autoDreamEnabled showThinkingSummaries skipDangerousModePermissionPrompt skipAutoPermissionPrompt useAutoModeDuringPlan autoMode disableAutoMode sshConfigs claudeMdExcludes pluginTrustMessage`)

func checkCapabilities(object protocol.JSONObject, rawFallback bool) error {
	for _, key := range activeKeys {
		found := false
		for _, property := range object.OwnProperties() {
			if bytes.Equal(property.NameJSON, []byte(`"`+key+`"`)) {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		switch key {
		case "useAutoModeDuringPlan", "autoMode", "disableAutoMode", "model", "env", "effortLevel", "enableAllProjectMcpServers", "enabledMcpjsonServers", "disabledMcpjsonServers", "skipDangerousModePermissionPrompt", "skipAutoPermissionPrompt", "pluginConfigs", "disableAllHooks", "allowManagedHooksOnly", "disableWorkflows", "enableWorkflows", "workflowKeywordTriggerEnabled", "builtInAgentOverrides", "strictPluginOnlyCustomization", "statusLine":
			continue
		case "cleanupPeriodDays":
			// The existing headless profile admits the genuine numeric-zero
			// setting that disables persistence. Other cleanup schema/runners
			// remain outside this finite parser, rather than passthrough keys.
			if receivedType(object.Get(key)) == "number" && bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(object.Get(key)), []byte("0")) {
				continue
			}
		case "permissions":
			if receivedType(object.Get(key)) == "array" {
				return &CapabilityError{"permissions array filter projection"}
			}
			if receivedType(object.Get(key)) == "object" {
				var permissions protocol.JSONObject
				_ = permissions.UnmarshalJSON(object.Get(key))
				for _, name := range []string{"allow", "deny", "ask"} {
					if permissions.Get(name) != nil {
						return &CapabilityError{"permission rule grammar " + name}
					}
				}
			}
			continue
		case "hooks":
			if receivedType(object.Get(key)) == "object" {
				if err := checkEmptyHooks(object.Get(key)); err != nil {
					return err
				}
				continue
			}
			if rawFallback && bytes.Equal(bytes.TrimSpace(object.Get(key)), []byte("123")) {
				continue
			}
		}
		return &CapabilityError{"active schema field " + key}
	}
	return nil
}
func parseJSONContent(content string) json.RawMessage {
	content = strings.TrimPrefix(content, "\ufeff")
	if !json.Valid([]byte(content)) {
		return nil
	}
	return bytes.TrimSpace([]byte(content))
}
func trimJS(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
}
func receivedType(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "undefined"
	}
	switch raw[0] {
	case 'n':
		return "null"
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	default:
		return "number"
	}
}
func validationError(file, path, expected, received string) json.RawMessage {
	message := "Expected " + expected + ", but received " + received
	if path == "" && expected == "object" && received == "null" {
		message = "Invalid or malformed JSON"
	}
	fields := []protocol.JSONField{{Name: "file", Value: quoted(file)}, {Name: "path", Value: quoted(path)}, {Name: "message", Value: quoted(message)}, {Name: "invalidValue", Value: quoted(received)}}
	if strings.HasPrefix(path, "env.") {
		fields = append(fields, protocol.JSONField{Name: "suggestion", Value: quoted(`Environment variables must be strings. Wrap numbers and booleans in quotes. Example: "DEBUG": "true", "PORT": "3000"`)}, protocol.JSONField{Name: "docLink", Value: quoted("https://code.claude.com/docs/en/settings#environment-variables")})
	}
	if strings.Contains(path, "hooks") {
		fields = append(fields, protocol.JSONField{Name: "suggestion", Value: quoted(`Hooks use a matcher + hooks array. The matcher is a string: a tool name ("Bash"), pipe-separated list ("Edit|Write"), or empty to match all. Example: {"PostToolUse": [{"matcher": "Edit|Write", "hooks": [{"type": "command", "command": "echo Done"}]}]}`)}, protocol.JSONField{Name: "docLink", Value: quoted("https://code.claude.com/docs/en/hooks")})
	}
	if expected == "boolean" {
		fields = append(fields, protocol.JSONField{Name: "suggestion", Value: quoted(`Use true or false without quotes. Example: "includeGitInstructions": true`)})
	}
	if path == "apiKeyHelper" {
		fields = append(fields, protocol.JSONField{Name: "suggestion", Value: quoted(`Provide a shell command that outputs your API key to stdout. The script should output only the API key. Example: "/bin/generate_temp_api_key.sh"`)})
	}
	if path == "permissions.additionalDirectories" {
		fields = append(fields, protocol.JSONField{Name: "suggestion", Value: quoted(`Must be an array of directory paths. Example: ["~/projects", "/tmp/workspace"]. You can also use --add-dir flag or /add-dir command`)}, protocol.JSONField{Name: "docLink", Value: quoted("https://code.claude.com/docs/en/iam#working-directories")})
	}
	o := protocol.NewJSONObject(fields...)
	raw, _ := o.MarshalJSON()
	return raw
}
func quoted(s string) json.RawMessage { raw, _ := json.Marshal(s); return raw }

// ValidateJSONContent only admits the explicit finite release fields. Invalid
// supported values return the actual Source validation projection; unsupported
// known capabilities return an error and cannot enter writer raw fallback.
func ValidateJSONContent(content, file string) (*ParsedSettings, error) {
	result := &ParsedSettings{Errors: []json.RawMessage{}}
	raw := parseJSONContent(content)
	if raw == nil {
		raw = []byte("null")
	}
	if receivedType(raw) != "object" {
		result.Errors = append(result.Errors, validationError(file, "", "object", receivedType(raw)))
		return result, nil
	}
	var input protocol.JSONObject
	_ = input.UnmarshalJSON(raw)
	if err := checkCapabilities(input, true); err != nil {
		return nil, err
	}
	if err := checkMergeValues(input); err != nil {
		return nil, err
	}
	out := protocol.NewJSONObject()
	if cleanup := input.Get("cleanupPeriodDays"); cleanup != nil {
		out.Set("cleanupPeriodDays", cleanup)
	}
	if env := input.Get("env"); env != nil {
		if receivedType(env) != "object" {
			result.Errors = append(result.Errors, validationError(file, "env", "record", receivedType(env)))
		} else {
			var record protocol.JSONObject
			_ = record.UnmarshalJSON(env)
			converted := protocol.NewJSONObject()
			for _, property := range record.OwnProperties() {
				value, err := protocol.ExactJavaScriptStringJSON(property.Value)
				if err != nil {
					// Property text must remain quoted UTF-16 through the diagnostic path.
					units, _ := protocol.DecodeJavaScriptJSONString(property.NameJSON)
					prefix, _ := protocol.DecodeJavaScriptJSONString(quoted("env."))
					pathJSON := protocol.QuoteJavaScriptUTF16(append(prefix, units...))
					e := validationError(file, "env.placeholder", "string", receivedType(property.Value))
					var diagnostic protocol.JSONObject
					_ = diagnostic.UnmarshalJSON(e)
					diagnostic.Set("path", pathJSON)
					e, _ = diagnostic.MarshalJSON()
					result.Errors = append(result.Errors, e)
				} else {
					_ = converted.SetPropertyJSON(property.NameJSON, value)
				}
			}
			value, _ := converted.MarshalJSON()
			out.Set("env", value)
		}
	}
	parsePermissions(input, &out, result, file)
	parseStatusLine(input, &out, result, file)
	if model := input.Get("model"); model != nil {
		if receivedType(model) != "string" {
			result.Errors = append(result.Errors, validationError(file, "model", "string", receivedType(model)))
		} else {
			out.Set("model", model)
		}
	}
	parseMigrationFields(input, &out, result, file, []string{"enableAllProjectMcpServers", "enabledMcpjsonServers", "disabledMcpjsonServers"})
	parseEmptyHooks(input, &out, result, file)
	parseWorkflowFields(input, &out, result, file)
	if effort := input.Get("effortLevel"); effort != nil {
		effort = protocol.StringifyValidatedJavaScriptJSON(effort)
		if bytes.Equal(effort, []byte(`"low"`)) || bytes.Equal(effort, []byte(`"medium"`)) || bytes.Equal(effort, []byte(`"high"`)) {
			out.Set("effortLevel", effort)
		} else {
			out.Set("effortLevel", nil)
		}
	}
	parseMigrationFields(input, &out, result, file, []string{"skipDangerousModePermissionPrompt", "skipAutoPermissionPrompt"})
	parseAutoSettings(input, &out, result, file)
	parsePluginConfigs(input, &out, result, file)
	parseAgentSettings(input, &out)
	for _, property := range input.OwnProperties() {
		if autoSettingsKey(property.NameJSON) || processedAgentProperty(property.NameJSON) {
			continue
		}
		if processedWorkflowKey(property.NameJSON) || processedEmptyHooksKey(property.NameJSON) || processedPluginKey(property.NameJSON) || processedMigrationKey(property.NameJSON) || bytes.Equal(property.NameJSON, []byte(`"env"`)) || bytes.Equal(property.NameJSON, []byte(`"model"`)) || bytes.Equal(property.NameJSON, []byte(`"cleanupPeriodDays"`)) || bytes.Equal(property.NameJSON, []byte(`"hooks"`)) || bytes.Equal(property.NameJSON, []byte(`"effortLevel"`)) || bytes.Equal(property.NameJSON, []byte(`"statusLine"`)) {
			continue
		}
		_ = out.SetPropertyJSON(property.NameJSON, property.Value)
	}
	if len(result.Errors) == 0 {
		result.Settings = &out
	}
	return result, nil
}

// IsActiveReleaseProperty uses the same original SettingsSchema recipe as the
// parser. Keys are raw quoted UTF-16 identities, not lossy Go string lookup.
func IsActiveReleaseProperty(name json.RawMessage) bool {
	units, err := protocol.DecodeJavaScriptJSONString(name)
	if err != nil {
		return true
	}
	canonical := protocol.QuoteJavaScriptUTF16(units)
	for _, key := range activeKeys {
		if bytes.Equal(canonical, quoted(key)) {
			return true
		}
	}
	return false
}
