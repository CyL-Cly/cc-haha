// Package sdkpermissions ports the limited original permissionSetup initialization
// used by external bare, empty-tools SDK sessions. It does not execute tools.
package sdkpermissions

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
)

const PublicPrintProfile = "source-normal-print-explicit-registry-v1"

const NeutralBareProfile = "external-release-bare-default-empty-tools-neutral-settings-v1"

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "SDK permission initialization capability is not migrated: " + e.Boundary
}
func cap(b string) error             { return &CapabilityError{b} }
func quote(s string) json.RawMessage { b, _ := json.Marshal(s); return b }

type Input struct {
	NativeStatusLineBindings                                 bool
	NativeAutoDreamBindings                                  bool
	NativeThinkingSummaryBindings                            bool
	NativeSpinnerBindings                                    bool
	NativeMainAgentBindings                                  bool
	NativeModelCommandBindings                               bool
	NativeSandboxBindings                                    bool
	Profile, Platform, Model                                 string
	NativeProcess                                            bool
	NativeDefaultCatalogBindings                             bool
	NativePluginBindings                                     bool
	NativeMCPBindings                                        bool
	NativePlanBindings                                       bool
	NativeWebFetchBindings                                   bool
	NativeWebSearchBindings                                  bool
	NativeSkillBindings                                      bool
	NativeMCPResourceBindings                                bool
	NativeToolSearchBindings                                 bool
	NativeSessionCollaborationBindings                       bool
	NativeWorkflowBindings                                   bool
	NativeTeamToolBindings                                   bool
	NativeLSPBindings                                        bool
	NativeImageBindings                                      bool
	NativeImageEnabled                                       bool
	NativeTeamExecutionBindings                              bool
	NativeWorktreeBindings                                   bool
	NativeBackgroundBindings                                 bool
	NativeHookBindings                                       bool
	NativeSystemContextBindings                              bool
	NativeAuthHelperBindings                                 bool
	NativeCloudSettingsBindings                              bool
	NativeWorkflowKeywordBindings                            bool
	NativeInternalReadBindings                               bool
	NativeStartup                                            *NativeStartup
	PermissionRules                                          map[string]RuleSet
	NonInteractive                                           bool
	BaseToolsCLI, Allowed, Disallowed, AdditionalDirectories []string
	Mode                                                     string
	AllowBypass                                              bool
}

type RuleSet struct {
	Allow, Deny, Ask []string
	// Only original lone-unit Skill contents are retained, at their original
	// array indexes. These transient inputs are never a persisted schema field.
	RawSkillAllow []json.RawMessage `json:"-"`
	RawSkillDeny  []json.RawMessage `json:"-"`
	RawSkillAsk   []json.RawMessage `json:"-"`
}

func (rules RuleSet) nativeRulesJSON(key string, normalizeEmpty bool) json.RawMessage {
	list, originals := rules.Allow, rules.RawSkillAllow
	if key == "alwaysDenyRules" {
		list, originals = rules.Deny, rules.RawSkillDeny
	} else if key == "alwaysAskRules" {
		list, originals = rules.Ask, rules.RawSkillAsk
	}
	if list == nil && !normalizeEmpty {
		return json.RawMessage("null")
	}
	values := make([]json.RawMessage, 0, len(list))
	for i, value := range list {
		raw := quote(value)
		if i < len(originals) && originals[i] != nil {
			parsed, err := ParsePermissionRuleValue(originals[i])
			var display string
			decodeErr := json.Unmarshal(originals[i], &display)
			if err == nil && decodeErr == nil && display == value && parsed.String("toolName") == "Skill" && parsed.Get("ruleContent") != nil {
				raw = originals[i]
			}
		}
		values = append(values, raw)
	}
	encoded, _ := json.Marshal(values)
	return encoded
}

type State struct {
	nativeStatusLineBindings      bool
	nativeAutoDreamBindings       bool
	nativeThinkingSummaryBindings bool
	nativePlanBindings            bool
	nativeSpinnerBindings         bool
	autoModeActive                bool
	autoFastMode                  bool
	nativeMainAgentBindings       bool
	nativeModelCommandBindings    bool
	nativeSandboxBindings         bool
	owner                         *cooperative.Owner
	settings                      *protocol.JSONObject
	context                       protocol.JSONObject
	nativeProcess                 bool
	nativePluginBindings          bool
	nativeMCPBindings             bool
	nativeWebFetchBindings        bool
	nativeWebSearchBindings       bool
	nativeWorktreeBindings        bool
	nativeHookBindings            bool
	nativeSystemContextBindings   bool
	nativeTeamExecutionBindings   bool
	nativeAuthHelperBindings      bool
	nativeCloudSettingsBindings   bool
	nativeWorkflowKeywordBindings bool
	nativeInternalReadBindings    bool
	nativeStartup                 *NativeStartup
}

func (s *State) Owns(owner *cooperative.Owner) bool {
	return s != nil && owner != nil && s.owner == owner
}

// SetMode is a real owner-bound permission transition. Dialog/SDK/desktop
// callers retain all loaded rules and directory identities when changing mode.
func (s *State) SetMode(scope *cooperative.Scope, mode string) error {
	if s == nil || !scope.BelongsTo(s.owner) {
		return cooperative.ErrRestricted
	}
	if e := scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil }); e != nil {
		return e
	}
	switch mode {
	case "default", "acceptEdits", "plan", "bypassPermissions", "dontAsk":
	case "auto":
		if s.nativeStartup == nil || s.nativeStartup.options.SettingsForSource == nil {
			return cap("permission mode auto without native policy")
		}
	default:
		return cap("permission mode " + mode)
	}
	if mode == "bypassPermissions" && string(s.context.Get("isBypassPermissionsModeAvailable")) != "true" {
		return cap("bypass permissions unavailable")
	}
	if s.nativeProcess && mode != s.context.String("mode") {
		if mode == "plan" || s.context.String("mode") == "plan" {
			if e := s.checkPlainPlanTransition(scope); e != nil {
				return e
			}
		}
		from := s.context.String("mode")
		if mode == "plan" {
			return s.EnterPlanMode(scope)
		}
		if s.autoModeActive && mode != "auto" {
			s.autoModeActive = false
			if e := s.restoreAutoRules(); e != nil {
				return e
			}
		}
		if mode == "auto" && !s.autoModeActive {
			_, gate, e := s.planAutoPolicy()
			if e != nil {
				return e
			}
			if !gate {
				return cap("auto mode gate disabled")
			}
			config := s.nativeStartup.autoFeatureConfig()
			if s.nativeStartup.autoEnabledState() == "disabled" || startupTruthy(config.Get("disableFastMode")) && s.autoFastMode {
				return cap("auto mode gate disabled")
			}
			if e = s.stripAutoRules(); e != nil {
				return e
			}
			s.autoModeActive = true
		}
		if from == "plan" {
			s.context.Delete("prePlanMode")
		}
	}
	s.context.Set("mode", quote(mode))
	return nil
}

// ValidateNeutral rejects *presence* of active known fields. Unknown raw keys,
// including distinct lone UTF-16 keys, are not reinterpreted as capabilities.
func ValidateNeutral(settings *protocol.JSONObject) error {
	if settings == nil || !settings.IsObject() {
		return cap("original settings object")
	}
	for _, property := range settings.OwnProperties() {
		if !clisettings.IsActiveReleaseProperty(property.NameJSON) {
			continue
		}
		key := property.NameJSON
		if bytes.Equal(key, quote("disableWorkflows")) || bytes.Equal(key, quote("enableWorkflows")) {
			if bytes.Equal(property.Value, []byte("true")) || bytes.Equal(property.Value, []byte("false")) {
				continue
			}
			return cap("Workflow boolean schema")
		}
		return cap("active settings property " + string(key))
	}
	return nil
}
func Initialize(scope *cooperative.Scope, owner *cooperative.Owner, settings *protocol.JSONObject, gate *workflowcommands.Gate, input Input) (*State, error) {
	if owner == nil || !scope.BelongsTo(owner) {
		return nil, cooperative.ErrRestricted
	}
	validate := ValidateNeutral
	if input.NativeProcess {
		validate = func(settings *protocol.JSONObject) error {
			return ValidateNativeWithBindings(settings, input.NativePluginBindings, input.NativeMCPBindings, input.NativeWebFetchBindings, input.NativeWebSearchBindings, input.NativeStartup.Owns(owner), input.NativeWorktreeBindings, input.NativeHookBindings, input.NativeSystemContextBindings, input.NativeTeamExecutionBindings, input.NativeAuthHelperBindings, input.NativeCloudSettingsBindings, input.NativeWorkflowKeywordBindings, input.NativeInternalReadBindings, input.NativeSandboxBindings, input.NativeModelCommandBindings, input.NativeMainAgentBindings, input.NativePlanBindings, input.NativeSpinnerBindings, input.NativeThinkingSummaryBindings, input.NativeAutoDreamBindings, input.NativeStatusLineBindings)
		}
	}
	if err := validate(settings); err != nil {
		return nil, err
	}
	if input.Profile == PublicPrintProfile {
		return initializePrint(scope, owner, settings, gate, input)
	}
	if gate == nil || input.Profile != NeutralBareProfile || input.Platform != "darwin" || input.Model != "fixture-model" || !input.NonInteractive || input.Mode != "default" || input.AllowBypass || len(input.Allowed) != 0 || len(input.Disallowed) != 0 || len(input.AdditionalDirectories) != 0 || len(input.BaseToolsCLI) != 1 || input.BaseToolsCLI[0] != "" {
		return nil, cap("unresolved original registry/CLI/permission profile")
	}
	// Source tools.ts: getAllBaseTools insertion order, buildTool default enabled.
	// This fixed release domain has false feature latches, USER_TYPE external,
	// non-Windows Bash, no embedded search, tasks v1 (noninteractive), no teams,
	// image credentials or channels. Worktree is unconditionally enabled. Model
	// WebSearch samples the default Claude bootstrap model before runHeadless
	// applies userSpecifiedModel; the requested fixture-model is not that getter.
	names := []string{"Agent", "TaskOutput", "Bash", "Glob", "Grep", "ExitPlanMode", "Read", "Edit", "Write", "NotebookEdit", "WebFetch", "TodoWrite", "WebSearch", "TaskStop", "AskUserQuestion", "Skill", "EnterPlanMode", "EnterWorktree", "ExitWorktree"}
	reason, err := gate.DisabledReason(scope)
	if err != nil {
		return nil, err
	}
	if reason == "" {
		names = append(names, "Workflow")
	}
	names = append(names, "ListMcpResourcesTool", "ReadMcpResourceTool")
	denied, _ := json.Marshal(names)
	// Source permissionSetup: empty base set denies every enabled tool. Disk
	// rules/addDirs are absent in this admitted neutral schema, no PWD alias.
	context := protocol.NewJSONObject(
		protocol.JSONField{Name: "mode", Value: quote(input.Mode)},
		protocol.JSONField{Name: "additionalWorkingDirectories", Value: []byte(`{"map":[]}`)},
		protocol.JSONField{Name: "alwaysAllowRules", Value: []byte(`{"cliArg":[]}`)},
		protocol.JSONField{Name: "alwaysDenyRules", Value: append(append([]byte(`{"cliArg":`), denied...), '}')},
		protocol.JSONField{Name: "alwaysAskRules", Value: []byte(`{}`)},
		protocol.JSONField{Name: "isBypassPermissionsModeAvailable", Value: []byte(`false`)},
		protocol.JSONField{Name: "isAutoModeAvailable", Value: []byte(`true`)})
	return &State{owner: owner, settings: settings, context: context}, nil
}
func (s *State) Read(scope *cooperative.Scope) (*protocol.JSONObject, protocol.JSONObject, error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return nil, protocol.JSONObject{}, cooperative.ErrRestricted
	}
	validate := ValidateNeutral
	if s.nativeProcess {
		validate = func(settings *protocol.JSONObject) error {
			return ValidateNativeWithBindings(settings, s.nativePluginBindings, s.nativeMCPBindings, s.nativeWebFetchBindings, s.nativeWebSearchBindings, s.nativeStartup.Owns(s.owner), s.nativeWorktreeBindings, s.nativeHookBindings, s.nativeSystemContextBindings, s.nativeTeamExecutionBindings, s.nativeAuthHelperBindings, s.nativeCloudSettingsBindings, s.nativeWorkflowKeywordBindings, s.nativeInternalReadBindings, s.nativeSandboxBindings, s.nativeModelCommandBindings, s.nativeMainAgentBindings, s.nativePlanBindings, s.nativeSpinnerBindings, s.nativeThinkingSummaryBindings, s.nativeAutoDreamBindings, s.nativeStatusLineBindings)
		}
	}
	if err := validate(s.settings); err != nil {
		return nil, protocol.JSONObject{}, err
	}
	return s.settings, s.context.Clone(), nil
}

// Native settings retain their raw object and known schema. Enabled hooks and
// plugin capabilities still require their actual consumers before admission.
func ValidateNative(settings *protocol.JSONObject) error {
	return ValidateNativeWithPlugins(settings, false)
}

func ValidateNativeWithPlugins(settings *protocol.JSONObject, pluginsBound bool) error {
	return ValidateNativeWithBindings(settings, pluginsBound, false)
}
func ValidateNativeWithBindings(settings *protocol.JSONObject, pluginsBound, mcpBound bool, webBindings ...bool) error {
	fetchBound := len(webBindings) > 0 && webBindings[0]
	searchBound := len(webBindings) > 1 && webBindings[1]
	startupBound := len(webBindings) > 2 && webBindings[2]
	worktreeBound := len(webBindings) > 3 && webBindings[3]
	hookBound := len(webBindings) > 4 && webBindings[4]
	systemContextBound := len(webBindings) > 5 && webBindings[5]
	teamExecutionBound := len(webBindings) > 6 && webBindings[6]
	authHelperBound := len(webBindings) > 7 && webBindings[7]
	cloudSettingsBound := len(webBindings) > 8 && webBindings[8]
	keywordBound := len(webBindings) > 9 && webBindings[9]
	internalReadBound := len(webBindings) > 10 && webBindings[10]
	sandboxBound := len(webBindings) > 11 && webBindings[11]
	modelCommandBound := len(webBindings) > 12 && webBindings[12]
	mainAgentBound := len(webBindings) > 13 && webBindings[13]
	planBound := len(webBindings) > 14 && webBindings[14]
	spinnerBound := len(webBindings) > 15 && webBindings[15]
	thinkingSummaryBound := len(webBindings) > 16 && webBindings[16]
	autoDreamBound := len(webBindings) > 17 && webBindings[17]
	statusLineBound := len(webBindings) > 18 && webBindings[18]
	if settings == nil || !settings.IsObject() {
		return cap("original native settings object")
	}
	for _, property := range settings.OwnProperties() {
		if bytes.Equal(property.NameJSON, quote("onlyManagedPermissionRules")) && string(property.Value) != "false" {
			return cap("native onlyManagedPermissionRules")
		}
		if !clisettings.IsActiveReleaseProperty(property.NameJSON) {
			continue
		}
		var key string
		_ = json.Unmarshal(property.NameJSON, &key)
		switch key {
		case "agent":
			if !mainAgentBound {
				return cap("agent setting without actual main-thread owner/catalog consumer")
			}
			if _, err := protocol.DecodeJavaScriptJSONString(property.Value); err != nil {
				return cap("agent setting original string schema")
			}
		case "attribution", "includeCoAuthoredBy":
			// Retired accept-and-ignore fields in Source SettingsSchema.
			// They intentionally require no commit/PR producer.
			continue
		case "hooks", "disableAllHooks", "allowManagedHooksOnly", "allowedHttpHookUrls", "httpHookAllowedEnvVars":
			if !hookBound {
				return cap("active native Hook setting without actual owner/settings/process consumer")
			}
		case "defaultShell":
			if !startupBound {
				return cap("active native defaultShell without actual startup/settings consumer")
			}
			value := string(protocol.StringifyValidatedJavaScriptJSON(property.Value))
			if value != `"bash"` && value != `"powershell"` {
				return cap("native defaultShell schema")
			}
		case "worktree":
			if !worktreeBound {
				return cap("active native Worktree setting without actual session/git/cwd/persistence bindings")
			}
		case "includeGitInstructions":
			if !systemContextBound {
				return cap("active native Git instructions without actual system context producer")
			}
		case "skipWebFetchPreflight":
			if !fetchBound {
				return cap("active native WebFetch setting without actual bindings")
			}
		case "webSearch":
			if !searchBound {
				return cap("active native WebSearch setting without actual bindings")
			}
		case "allowedMcpServers", "deniedMcpServers", "allowManagedMcpServersOnly":
			if !mcpBound {
				return cap("active native settings consumer " + key)
			}
		case "enabledPlugins", "extraKnownMarketplaces", "strictKnownMarketplaces", "blockedMarketplaces", "pluginConfigs":
			if !pluginsBound {
				return cap("active native settings consumer " + key)
			}
		case "autoMemoryDirectory":
			if !internalReadBound {
				return cap("autoMemoryDirectory actual memory/internal-read bindings")
			}
			if _, err := protocol.DecodeJavaScriptJSONString(property.Value); err != nil {
				return cap("autoMemoryDirectory original string schema")
			}
		case "claudeMdExcludes", "autoMemoryEnabled":
			if !internalReadBound || !systemContextBound {
				return cap(key + " actual memory/system-context bindings")
			}
		case "plansDirectory":
			if !planBound {
				return cap("plansDirectory actual session plan bindings")
			}
		case "spinnerVerbs":
			if !spinnerBound {
				return cap("spinnerVerbs actual spinner selection bindings")
			}
		case "autoDreamEnabled":
			if !autoDreamBound {
				return cap("autoDreamEnabled actual scheduler and skipTranscript fork bindings")
			}
		case "showThinkingSummaries":
			if !thinkingSummaryBound {
				return cap("showThinkingSummaries actual runtime model beta bindings")
			}
		case "effortLevel":
			// Native main reads this through getInitialEffortSetting's external
			// toPersistableEffort filter. CLI/session-only levels are not disk
			// defaults; schema .catch(undefined) has already removed invalid ones.
			value := string(protocol.StringifyValidatedJavaScriptJSON(property.Value))
			if value != `"low"` && value != `"medium"` && value != `"high"` {
				return cap("native persisted effortLevel")
			}
		case "maxConcurrentSubagents":
			if !clisettings.IsNativeSubagentLimit(property.Value) {
				return cap("native maxConcurrentSubagents positive safe integer or null")
			}
		case "cleanupPeriodDays":
			if !clisettings.IsNativeCleanupPeriodDays(property.Value) {
				return cap("native cleanupPeriodDays nonnegative safe integer")
			}
		case "availableModels":
			var models []json.RawMessage
			if len(property.Value) == 0 || property.Value[0] != '[' || json.Unmarshal(property.Value, &models) != nil {
				return cap("native availableModels string array")
			}
			for _, model := range models {
				if _, err := protocol.DecodeJavaScriptJSONString(model); err != nil {
					return cap("native availableModels string array")
				}
			}
		case "modelOverrides":
			var overrides protocol.JSONObject
			if overrides.UnmarshalJSON(property.Value) != nil || !overrides.IsObject() {
				return cap("native modelOverrides string record")
			}
			for _, value := range overrides.OwnProperties() {
				if _, err := protocol.DecodeJavaScriptJSONString(value.Value); err != nil {
					return cap("native modelOverrides string record")
				}
			}
		case "language", "outputStyle":
			if _, e := protocol.DecodeJavaScriptJSONString(property.Value); e != nil {
				return cap("native " + key + " string")
			}
		case "alwaysThinkingEnabled", "respectGitignore":
			value := string(protocol.StringifyValidatedJavaScriptJSON(property.Value))
			if value != "true" && value != "false" {
				return cap("native " + key + " boolean")
			}
		case "autoMode", "disableAutoMode":
			if !startupBound {
				return cap("native auto policy without actual startup")
			}
			if key == "autoMode" {
				if len(property.Value) == 0 || property.Value[0] != '{' {
					return cap("native autoMode schema")
				}
			} else if string(property.Value) != `"disable"` {
				return cap("native disableAutoMode schema")
			}
		case "useAutoModeDuringPlan", "skipAutoPermissionPrompt":
			// Persistent opt-in does not select auto mode. Source's ordinary
			// default/bypass startup remains valid with this setting present.
			// Actual auto entry and auto-plan still require their consumers.
			if !startupBound {
				return cap("native auto opt-in without actual startup consumer")
			}
			value := string(protocol.StringifyValidatedJavaScriptJSON(property.Value))
			if value != "true" && value != "false" {
				return cap("native skipAutoPermissionPrompt boolean")
			}
		case "agentTeamsEnabled":
			value := string(protocol.StringifyValidatedJavaScriptJSON(property.Value))
			if value != "false" && (value != "true" || !teamExecutionBound) {
				return cap("native active agentTeamsEnabled consumer")
			}
		case "permissions":
			var permissions protocol.JSONObject
			if e := permissions.UnmarshalJSON(property.Value); e != nil || !permissions.IsObject() {
				return cap("native permissions object")
			}
			for _, field := range permissions.OwnProperties() {
				var name string
				_ = json.Unmarshal(field.NameJSON, &name)
				switch name {
				case "additionalDirectories":
					var dirs []string
					if e := json.Unmarshal(field.Value, &dirs); e != nil || len(dirs) > 0 && !startupBound {
						return cap("native permissions.additionalDirectories")
					}
				case "defaultMode":
					if !startupBound {
						return cap("native permissions.defaultMode startup policy consumer")
					}
					var mode string
					_ = json.Unmarshal(field.Value, &mode)
					switch mode {
					case "default", "acceptEdits", "plan", "bypassPermissions", "dontAsk", "auto":
					default:
						return cap("native permissions.defaultMode schema")
					}
				case "disableBypassPermissionsMode", "disableAutoMode":
					if !startupBound || string(field.Value) != `"disable"` {
						return cap("native permissions." + name + " startup policy consumer")
					}
				case "onlyManagedPermissionRules":
					if string(field.Value) != "false" {
						return cap("native permissions.onlyManagedPermissionRules")
					}
				}
			}
		case "onlyManagedPermissionRules":
			if string(property.Value) != "false" {
				return cap("native onlyManagedPermissionRules")
			}
		case "apiKeyHelper":
			if !authHelperBound {
				return cap("apiKeyHelper actual process/cache producer")
			}
			if _, err := protocol.DecodeJavaScriptJSONString(property.Value); err != nil {
				return cap("apiKeyHelper original string schema")
			}
		case "awsCredentialExport", "awsAuthRefresh", "gcpAuthRefresh":
			if !cloudSettingsBound {
				return cap("AWS/GCP credential settings require inactive-provider and actual cache bindings")
			}
			if _, err := protocol.DecodeJavaScriptJSONString(property.Value); err != nil {
				return cap("original cloud helper string schema")
			}
		case "fastMode", "fastModePerSessionOptIn":
			if !modelCommandBound {
				return cap("active native model command setting without actual owner/state/runtime consumer")
			}
			if string(property.Value) != "true" && string(property.Value) != "false" {
				return cap("native model command boolean schema")
			}
		case "workflowKeywordTriggerEnabled":
			if !keywordBound {
				return cap("workflow keyword setting without actual interactive state/attachment/Workflow bindings")
			}
			if string(property.Value) != "true" && string(property.Value) != "false" {
				return cap("workflow keyword boolean schema")
			}
		case "spinnerTipsEnabled", "prefersReducedMotion", "syntaxHighlightingDisabled":
			// Presentation preferences are consumed by the shared native
			// startup snapshot and terminal renderer, outside permission policy.
			// Headless operation retains the same files without rendering tips.
			if !startupBound || !spinnerBound {
				return cap("native terminal preferences require actual startup/spinner bindings")
			}
			if string(property.Value) != "true" && string(property.Value) != "false" {
				return cap("native terminal preference boolean schema")
			}
		case "statusLine":
			if !startupBound || !statusLineBound || !hookBound {
				return cap("statusLine requires actual native settings/shell/component bindings")
			}
			var value protocol.JSONObject
			if value.UnmarshalJSON(property.Value) != nil || value.String("type") != "command" {
				return cap("statusLine original command schema")
			}
			if _, err := protocol.DecodeJavaScriptJSONString(value.Get("command")); err != nil {
				return cap("statusLine original command schema")
			}
		case "companyAnnouncements":
			if !startupBound {
				return cap("companyAnnouncements requires actual startup/export bindings")
			}
			var entries []json.RawMessage
			if len(property.Value) == 0 || property.Value[0] != '[' || json.Unmarshal(property.Value, &entries) != nil {
				return cap("companyAnnouncements original string array schema")
			}
			for _, entry := range entries {
				if _, err := protocol.DecodeJavaScriptJSONString(entry); err != nil {
					return cap("companyAnnouncements original string array schema")
				}
			}
		case "autoUpdatesChannel", "minimumVersion":
			// These settings belong to the native installer. The ordinary TUI
			// updater and explicit CLI install/update consume the same parsed
			// startup store; permission policy adds no installation semantics.
			if !startupBound || !clisettings.IsNativeInstallationSetting(key, property.Value) {
				return cap("native installation settings require actual startup/updater bindings and schema")
			}
		case "sandbox":
			if !sandboxBound {
				return cap("active native Sandbox setting without actual owner/runtime/command bindings")
			}
		case "env", "model", "enableWorkflows", "disableWorkflows", "strictPluginOnlyCustomization", "builtinAgentOverrides":
		default:
			return cap("active native settings consumer " + key)
		}
	}
	return nil
}
