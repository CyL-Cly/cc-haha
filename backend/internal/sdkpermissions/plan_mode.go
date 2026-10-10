package sdkpermissions

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *State) checkPlainPlanTransition(scope *cooperative.Scope) error {
	if s == nil || !scope.BelongsTo(s.owner) {
		return cooperative.ErrRestricted
	}
	if !s.nativeProcess {
		return cap("native shared PlanMode permission context")
	}
	if (s.nativeStartup == nil || s.nativeStartup.options.SettingsForSource == nil) && (s.context.String("mode") == "auto" || s.context.String("prePlanMode") == "auto" || s.context.Get("strippedDangerousRules") != nil) {
		return cap("PlanMode auto classifier and dangerous-rule restoration")
	}
	return scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil })
}

// EnterPlanMode is prepareContextForPlanMode plus the original session setMode
// update. Auto-mode state is resolved from the actual trusted source settings
// bound by NativeStartup; an unbound context retains the explicit capability.
func (s *State) EnterPlanMode(scope *cooperative.Scope) error {
	if e := s.checkPlainPlanTransition(scope); e != nil {
		return e
	}
	if s.context.String("mode") != "plan" {
		want, _, e := s.planAutoPolicy()
		if e != nil {
			return e
		}
		if s.context.String("mode") == "auto" {
			if !want {
				s.autoModeActive = false
				if e = s.restoreAutoRules(); e != nil {
					return e
				}
			}
		} else if want && s.context.String("mode") != "bypassPermissions" {
			if e = s.stripAutoRules(); e != nil {
				return e
			}
			s.autoModeActive = true
		}
		s.context.Set("prePlanMode", quote(s.context.String("mode")))
	}
	s.context.Set("mode", quote("plan"))
	return nil
}
func (s *State) ExitPlanMode(scope *cooperative.Scope) (bool, error) {
	if e := s.checkPlainPlanTransition(scope); e != nil {
		return false, e
	}
	if s.context.String("mode") != "plan" {
		return false, nil
	}
	mode := s.context.String("prePlanMode")
	if mode == "" {
		mode = "default"
	}
	if mode == "auto" {
		_, gate, e := s.planAutoPolicy()
		if e != nil {
			return false, e
		}
		if !gate {
			mode = "default"
		}
	}
	s.autoModeActive = mode == "auto"
	if !s.autoModeActive {
		if e := s.restoreAutoRules(); e != nil {
			return false, e
		}
	}
	if e := s.SetMode(scope, mode); e != nil {
		return false, e
	}
	s.context.Delete("prePlanMode")
	return true, nil
}
func (s *State) PlanModeContext(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	return s.context.Clone(), nil
}
