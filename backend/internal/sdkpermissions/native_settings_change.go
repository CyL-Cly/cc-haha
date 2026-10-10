package sdkpermissions

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ApplyNativeSettingsChange is applySettingsChange's live permission consumer.
// Session/CLI rules survive the disk sync; disabling bypass is one-way for the
// current session. Unsupported classifier/ant transitions remain explicit.
func (s *State) ApplyNativeSettingsChange(scope *cooperative.Scope, settings *protocol.JSONObject, rules map[string]RuleSet) error {
	if s == nil || !s.nativeProcess || !scope.BelongsTo(s.owner) || !s.nativeStartup.Owns(s.owner) {
		return cooperative.ErrRestricted
	}
	if s.nativeStartup.options.Environment("USER_TYPE") == "ant" && s.nativeStartup.options.Environment("CLAUDE_CODE_ENTRYPOINT") != "local-agent" {
		return cap("settings-change ant overly broad Bash rule preparation")
	}
	if s.context.String("mode") == "auto" {
		return cap("settings-change active auto classifier rule reconciliation")
	}

	disable := false
	if s.nativeStartup.options.Policy == nil {
		return cap("settings-change original policy getter")
	}
	policy, err := s.nativeStartup.options.Policy()
	if err != nil {
		return err
	}
	managedOnly := policy != nil && string(policy.Get("allowManagedPermissionRulesOnly")) == "true"
	previous := s.context.Clone()
	if string(s.context.Get("isBypassPermissionsModeAvailable")) == "true" {
		gate, err := s.nativeStartup.cachedDisableBypass()
		if err != nil {
			return err
		}
		disable = gate || startupPermissions(settings).String("disableBypassPermissionsMode") == "disable"
	}
	if err := s.ReloadSettings(scope, settings, rules); err != nil {
		return err
	}
	// The real syncPermissionRulesFromDisk clears each synchronized source to
	// [] in place. Deleting keys or marshalling nil slices changes its shape,
	// insertion order and retained CLI/session provenance.
	next := previous.Clone()
	for _, key := range []string{"alwaysAllowRules", "alwaysDenyRules", "alwaysAskRules"} {
		var values protocol.JSONObject
		if err := values.UnmarshalJSON(previous.Get(key)); err != nil {
			return err
		}
		if managedOnly {
			for _, source := range []string{"userSettings", "projectSettings", "localSettings", "cliArg", "session"} {
				values.Set(source, []byte("[]"))
			}
		}
		for _, source := range []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings"} {
			values.Set(source, []byte("[]"))
		}
		for _, source := range []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings"} {
			if managedOnly && source != "policySettings" {
				continue
			}
			rule, exists := rules[source]
			if !exists {
				continue
			}
			var list []json.RawMessage
			if err := json.Unmarshal(rule.nativeRulesJSON(key, true), &list); err != nil {
				return err
			}
			parsed := []protocol.JSONObject{}
			for _, value := range list {
				rule, err := ParsePermissionRuleValue(value)
				if err != nil {
					return err
				}
				parsed = append(parsed, rule)
			}
			encoded, _ := json.Marshal(parsed)
			formatted, err := permissionUpdateRules(protocol.NewJSONObject(protocol.JSONField{Name: "rules", Value: encoded}))
			if err != nil {
				return err
			}
			raw, _ := json.Marshal(formatted)
			values.Set(source, raw)
		}
		raw, err := values.MarshalJSON()
		if err != nil {
			return err
		}
		next.Set(key, raw)
	}
	s.context = next
	if disable {
		next := s.context.Clone()
		if next.String("mode") == "bypassPermissions" {
			next.Set("mode", quote("default"))
		}
		next.Set("isBypassPermissionsModeAvailable", []byte("false"))
		s.context = next
	}
	if e := s.reconcileAutoRules(); e != nil {
		return e
	}
	if s.context.String("mode") == "plan" && s.context.String("prePlanMode") != "bypassPermissions" {
		want, _, e := s.planAutoPolicy()
		if e != nil {
			return e
		}
		if want {
			s.autoModeActive = true
			if e = s.stripAutoRules(); e != nil {
				return e
			}
		} else if s.autoModeActive {
			s.autoModeActive = false
			if e = s.restoreAutoRules(); e != nil {
				return e
			}
		}
	}
	return nil
}
