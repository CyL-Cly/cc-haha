package sdkpermissions

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This distribution has no external GrowthBook key. Original ant overrides
// retain their precedence; arbitrary cached disk features cannot enable a gate.
func (s *NativeStartup) autoFeatureConfig() protocol.JSONObject {
	config := protocol.NewJSONObject()
	if s == nil || s.options.Environment == nil || s.options.Environment("USER_TYPE") != "ant" {
		return config
	}
	var overrides protocol.JSONObject
	if overrides.UnmarshalJSON([]byte(s.options.Environment("CLAUDE_INTERNAL_FC_OVERRIDES"))) == nil && overrides.Get("tengu_auto_mode_config") != nil {
		_ = config.UnmarshalJSON(overrides.Get("tengu_auto_mode_config"))
		return config
	}
	global, err := s.options.Global()
	if err == nil && global != nil {
		_ = overrides.UnmarshalJSON(global.Get("growthBookOverrides"))
		_ = config.UnmarshalJSON(overrides.Get("tengu_auto_mode_config"))
	}
	return config
}
func (s *NativeStartup) autoEnabledState() string {
	switch value := s.autoFeatureConfig().String("enabled"); value {
	case "enabled", "disabled", "opt-in":
		return value
	}
	return "opt-in"
}
func (s *State) stripStartupAutoRules() error {
	all, err := s.autoConfig()
	if err != nil {
		return err
	}
	allowed, _ := s.context.Object("alwaysAllowRules")
	for _, property := range allowed.OwnProperties() {
		var rules []json.RawMessage
		if err = json.Unmarshal(property.Value, &rules); err != nil {
			return err
		}
		for _, raw := range rules {
			rule, err := ParsePermissionRuleValue(raw)
			if err != nil {
				return err
			}
			if dangerousAutoRule(rule, all) {
				return s.stripAutoRules()
			}
		}
	}
	return nil
}

func (s *State) AutoModeOptIn(scope *cooperative.Scope) (bool, error) {
	if s == nil || !scope.BelongsTo(s.owner) || s.nativeStartup == nil || s.nativeStartup.options.SettingsForSource == nil {
		return false, cooperative.ErrRestricted
	}
	for _, source := range []string{"userSettings", "flagSettings", "policySettings"} {
		settings, err := s.nativeStartup.options.SettingsForSource(source)
		if err != nil {
			return false, err
		}
		if settings != nil && startupTruthy(settings.Get("skipAutoPermissionPrompt")) {
			return true, nil
		}
	}
	return false, nil
}

// VerifyAutoModeGate applies the original fresh gate to the current context,
// including plan's active classifier. Explicit entry is a session opt-in;
// only trusted persistent settings or the CLI flag expose the carousel.
func (s *State) VerifyAutoModeGate(scope *cooperative.Scope, cliFlag, fastMode bool) (string, bool, error) {
	if s == nil || !scope.BelongsTo(s.owner) || s.nativeStartup == nil {
		return "", false, cooperative.ErrRestricted
	}
	_, settingsGate, err := s.planAutoPolicy()
	if err != nil {
		return "", false, err
	}
	config := s.nativeStartup.autoFeatureConfig()
	s.autoFastMode = fastMode
	enabled := s.nativeStartup.autoEnabledState()
	canEnter := settingsGate && enabled != "disabled" && !(startupTruthy(config.Get("disableFastMode")) && fastMode)
	opted, err := s.AutoModeOptIn(scope)
	if err != nil {
		return "", false, err
	}
	wasAvailable := string(s.context.Get("isAutoModeAvailable")) == "true"
	s.context.Set("isAutoModeAvailable", autoRaw(canEnter && (enabled == "enabled" || opted || cliFlag)))
	inAuto := s.context.String("mode") == "auto"
	inPlan := s.context.String("mode") == "plan" && (s.context.String("prePlanMode") == "auto" || s.context.Get("strippedDangerousRules") != nil)
	if canEnter {
		return "", false, nil
	}
	if inAuto || inPlan {
		s.autoModeActive = false
		if err = s.restoreAutoRules(); err != nil {
			return "", false, err
		}
		if inAuto {
			s.context.Set("mode", quote("default"))
		} else if s.context.String("prePlanMode") == "auto" {
			s.context.Set("prePlanMode", quote("default"))
		}
	} else if !cliFlag || !wasAvailable {
		return "", false, nil
	}
	notice := "auto mode unavailable for this model"
	if !settingsGate {
		notice = "auto mode disabled by settings"
	} else if enabled == "disabled" {
		notice = "auto mode is unavailable for your plan"
	}
	if s.nativeStartup.options.Environment("USER_TYPE") == "ant" {
		notice += " · #claude-code-feedback"
	}
	return notice, inAuto || inPlan, nil
}

func (s *State) NextTerminalMode(scope *cooperative.Scope, userType string) (string, error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return "", cooperative.ErrRestricted
	}
	_, gate, err := s.planAutoPolicy()
	if err != nil {
		return "", err
	}
	gate = gate && (s.nativeStartup == nil || s.nativeStartup.autoEnabledState() != "disabled" && !(startupTruthy(s.nativeStartup.autoFeatureConfig().Get("disableFastMode")) && s.autoFastMode))
	return NextTerminalMode(s.context, userType, gate), nil
}

func (s *State) DeclineAutoMode(scope *cooperative.Scope) error {
	if s == nil || !scope.BelongsTo(s.owner) {
		return cooperative.ErrRestricted
	}
	s.context.Set("isAutoModeAvailable", autoRaw(false))
	return nil
}
