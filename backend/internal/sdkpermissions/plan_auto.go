package sdkpermissions

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

// PlanAutoPolicy reads original settings at the same live entry/check boundary.
// It is bound by NativeStartup, never supplied as a precomputed permission mode.
func (s *State) planAutoPolicy() (bool, bool, error) {
	if s.nativeStartup == nil || s.nativeStartup.options.SettingsForSource == nil || s.nativeStartup.options.InitialSettings == nil {
		return false, true, nil
	}
	get := s.nativeStartup.options.SettingsForSource
	enabled := false
	for _, source := range []string{"userSettings", "flagSettings", "policySettings"} {
		o, e := get(source)
		if e != nil {
			return false, false, e
		}
		if o != nil && startupTruthy(o.Get("skipAutoPermissionPrompt")) {
			enabled = true
		}
	}
	merged, e := s.nativeStartup.options.InitialSettings()
	if e != nil {
		return false, false, e
	}
	gate := true
	if merged != nil {
		gate = merged.String("disableAutoMode") != "disable" && startupPermissions(merged).String("disableAutoMode") != "disable"
	}
	if !enabled || !gate {
		return false, gate, nil
	}
	for _, source := range []string{"policySettings", "flagSettings", "userSettings", "localSettings"} {
		o, e := get(source)
		if e != nil {
			return false, false, e
		}
		if o != nil && string(o.Get("useAutoModeDuringPlan")) == "false" {
			return false, gate, nil
		}
	}
	return true, gate, nil
}
func (s *State) AutoModeActive(scope *cooperative.Scope) (bool, error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return false, cooperative.ErrRestricted
	}
	return s.autoModeActive, nil
}
func (s *State) autoConfig() (bool, error) {
	if s.nativeStartup == nil || s.nativeStartup.options.SettingsForSource == nil {
		return false, nil
	}
	classify := false
	for _, name := range []string{"userSettings", "localSettings", "flagSettings", "policySettings"} {
		o, e := s.nativeStartup.options.SettingsForSource(name)
		if e != nil {
			return false, e
		}
		if o != nil {
			a, ok := o.Object("autoMode")
			if ok && a.Get("classifyAllShell") != nil {
				classify = string(a.Get("classifyAllShell")) == "true"
			}
		}
	}
	return classify, nil
}
func dangerousAutoRule(rule protocol.JSONObject, allShell bool) bool {
	name, content := rule.String("toolName"), strings.ToLower(strings.TrimSpace(rule.String("ruleContent")))
	if name == "Agent" {
		return true
	}
	if name != "Bash" && name != "PowerShell" {
		return false
	}
	if allShell || content == "" || content == "*" {
		return true
	}
	patterns := strings.Fields("python python3 python2 node deno tsx ruby perl php lua npx bunx bash sh ssh")
	patterns = append(patterns, "npm run", "yarn run", "pnpm run", "bun run")
	if name == "Bash" {
		patterns = append(patterns, strings.Fields("zsh fish eval exec env xargs sudo")...)
	} else {
		patterns = append(patterns, strings.Fields("pwsh powershell cmd wsl iex invoke-expression icm invoke-command start-process saps start start-job sajb start-threadjob register-objectevent register-engineevent register-wmievent register-scheduledjob new-pssession nsn enter-pssession etsn add-type new-object")...)
	}
	match := func(p string) bool {
		return content == p || content == p+":*" || content == p+"*" || content == p+" *" || strings.HasPrefix(content, p+" -") && strings.HasSuffix(content, "*")
	}
	for _, p := range patterns {
		if match(p) {
			return true
		}
		if name == "PowerShell" {
			first, rest, has := strings.Cut(p, " ")
			exe := first + ".exe"
			if has {
				exe += " " + rest
			}
			if match(exe) {
				return true
			}
		}
	}
	return false
}
func (s *State) stripAutoRules() error {
	all, e := s.autoConfig()
	if e != nil {
		return e
	}
	allowed, ok := s.context.Object("alwaysAllowRules")
	if !ok {
		allowed = protocol.NewJSONObject()
	}
	stash := protocol.NewJSONObject()
	found := false
	for _, property := range allowed.OwnProperties() {
		var label string
		_ = json.Unmarshal(property.NameJSON, &label)
		valid := label == "userSettings" || label == "projectSettings" || label == "localSettings" || label == "session" || label == "cliArg"
		var rules []json.RawMessage
		if e = json.Unmarshal(property.Value, &rules); e != nil {
			return e
		}
		kept, removed := []json.RawMessage{}, []json.RawMessage{}
		for _, r := range rules {
			v, e := ParsePermissionRuleValue(r)
			if e != nil {
				return e
			}
			if dangerousAutoRule(v, all) {
				found = true
				if valid {
					canonical, e := permissionUpdateRules(protocol.NewJSONObject(protocol.JSONField{Name: "rules", Value: autoRaw([]protocol.JSONObject{v})}))
					if e != nil {
						return e
					}
					removed = append(removed, canonical...)
					continue
				}
			}
			kept = append(kept, r)
		}
		if len(removed) > 0 {
			stash.SetPropertyJSON(property.NameJSON, autoRaw(removed))
			allowed.SetPropertyJSON(property.NameJSON, autoRaw(kept))
		}
	}
	if !found && s.context.Get("strippedDangerousRules") != nil {
		return nil
	}
	s.context.Set("alwaysAllowRules", autoRaw(allowed))
	s.context.Set("strippedDangerousRules", autoRaw(stash))
	return nil
}
func (s *State) restoreAutoRules() error {
	stash, ok := s.context.Object("strippedDangerousRules")
	if !ok {
		return nil
	}
	allowed, ok := s.context.Object("alwaysAllowRules")
	if !ok {
		allowed = protocol.NewJSONObject()
	}
	for _, p := range stash.OwnProperties() {
		var existing, add []json.RawMessage
		_ = json.Unmarshal(autoProperty(allowed, p.NameJSON), &existing)
		if e := json.Unmarshal(p.Value, &add); e != nil {
			return e
		}
		if len(add) == 0 {
			continue
		}
		seen := map[string]bool{}
		next := []json.RawMessage{}
		for _, r := range append(existing, add...) {
			id := rawStringIdentity(r)
			if !seen[id] {
				seen[id] = true
				next = append(next, r)
			}
		}
		allowed.SetPropertyJSON(p.NameJSON, autoRaw(next))
	}
	s.context.Set("alwaysAllowRules", autoRaw(allowed))
	s.context.Delete("strippedDangerousRules")
	return nil
}

func autoProperty(o protocol.JSONObject, name json.RawMessage) json.RawMessage {
	for _, p := range o.OwnProperties() {
		if rawStringIdentity(p.NameJSON) == rawStringIdentity(name) {
			return p.Value
		}
	}
	return nil
}

func autoRaw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// reconcileAutoRules implements the disk-reload stash replacement before the
// plan toggle. Removed disk permissions must never be restored on plan exit.
func (s *State) reconcileAutoRules() error {
	if !s.autoModeActive && s.context.String("mode") != "auto" {
		return nil
	}
	all, e := s.autoConfig()
	if e != nil {
		return e
	}
	old, _ := s.context.Object("strippedDangerousRules")
	allowed, _ := s.context.Object("alwaysAllowRules")
	stash := protocol.NewJSONObject()
	disk := func(source string) bool {
		return source == "userSettings" || source == "projectSettings" || source == "localSettings"
	}
	valid := func(source string) bool { return disk(source) || source == "session" || source == "cliArg" }
	add := func(source string, rule json.RawMessage) {
		var list []json.RawMessage
		_ = json.Unmarshal(stash.Get(source), &list)
		for _, v := range list {
			if rawStringIdentity(v) == rawStringIdentity(rule) {
				return
			}
		}
		list = append(list, rule)
		stash.Set(source, autoRaw(list))
	}
	for _, p := range old.OwnProperties() {
		var source string
		_ = json.Unmarshal(p.NameJSON, &source)
		if !valid(source) || disk(source) {
			continue
		}
		var list []json.RawMessage
		if e = json.Unmarshal(p.Value, &list); e != nil {
			return e
		}
		for _, r := range list {
			add(source, r)
		}
	}
	for _, pass := range []bool{false, true} {
		for _, p := range allowed.OwnProperties() {
			var source string
			_ = json.Unmarshal(p.NameJSON, &source)
			if !valid(source) || disk(source) != pass {
				continue
			}
			var list []json.RawMessage
			if e = json.Unmarshal(p.Value, &list); e != nil {
				return e
			}
			for _, r := range list {
				parsed, e := ParsePermissionRuleValue(r)
				if e != nil {
					return e
				}
				if dangerousAutoRule(parsed, all) {
					normalized, e := permissionUpdateRules(protocol.NewJSONObject(protocol.JSONField{Name: "rules", Value: autoRaw([]protocol.JSONObject{parsed})}))
					if e != nil {
						return e
					}
					for _, v := range normalized {
						add(source, v)
					}
				}
			}
		}
	}
	if e = s.stripAutoRules(); e != nil {
		return e
	}
	s.context.Set("strippedDangerousRules", autoRaw(stash))
	return nil
}
