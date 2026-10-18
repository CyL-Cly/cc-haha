package sdkpermissions

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ReloadSettings keeps live session/CLI rules and mode while replacing actual
// disk-source provenance. A disabled source must remove its previous rules.
func (s *State) ReloadSettings(scope *cooperative.Scope, settings *protocol.JSONObject, rules map[string]RuleSet) error {
	if s == nil || !s.nativeProcess || !scope.BelongsTo(s.owner) {
		return cooperative.ErrRestricted
	}
	if err := ValidateNativeWithBindings(settings, s.nativePluginBindings, s.nativeMCPBindings, s.nativeWebFetchBindings, s.nativeWebSearchBindings, s.nativeStartup.Owns(s.owner), s.nativeWorktreeBindings, s.nativeHookBindings, s.nativeSystemContextBindings, s.nativeTeamExecutionBindings, s.nativeAuthHelperBindings, s.nativeCloudSettingsBindings, s.nativeWorkflowKeywordBindings, s.nativeInternalReadBindings, s.nativeSandboxBindings, s.nativeModelCommandBindings, s.nativeMainAgentBindings, s.nativePlanBindings, s.nativeSpinnerBindings, s.nativeThinkingSummaryBindings, s.nativeAutoDreamBindings, s.nativeStatusLineBindings); err != nil {
		return err
	}
	next := s.context.Clone()
	for _, key := range []string{"alwaysAllowRules", "alwaysDenyRules", "alwaysAskRules"} {
		var values protocol.JSONObject
		if err := values.UnmarshalJSON(next.Get(key)); err != nil {
			return err
		}
		for _, source := range []string{"userSettings", "projectSettings", "localSettings", "policySettings", "flagSettings"} {
			values.Delete(source)
		}
		for source, rule := range rules {
			values.Set(source, rule.nativeRulesJSON(key, false))
		}
		data, err := values.MarshalJSON()
		if err != nil {
			return err
		}
		next.Set(key, data)
	}
	s.settings, s.context = settings, next
	return nil
}
