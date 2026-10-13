package sdkpermissions

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NativeSDKModeRejection mirrors print.handleSetPermissionMode's live guards.
// Failed requests preserve the current context and remain nonfatal SDK replies.
func (s *NativeStartup) NativeSDKModeRejection(scope *cooperative.Scope, mode string, current protocol.JSONObject) (string, error) {
	if s == nil || !scope.BelongsTo(s.owner) || s.prepared == nil {
		return "", cooperative.ErrRestricted
	}
	settings, err := s.options.InitialSettings()
	if err != nil {
		return "", err
	}
	if mode == "bypassPermissions" {
		gate, err := s.cachedDisableBypass()
		if err != nil {
			return "", err
		}
		if gate || startupPermissions(settings).String("disableBypassPermissionsMode") == "disable" {
			return "Cannot set permission mode to bypassPermissions because it is disabled by settings or configuration", nil
		}
		if string(current.Get("isBypassPermissionsModeAvailable")) != "true" {
			return "Cannot set permission mode to bypassPermissions because the session was not launched with --dangerously-skip-permissions", nil
		}
	}
	if mode == "auto" {
		reason := ""
		if settings != nil && (settings.String("disableAutoMode") == "disable" || startupPermissions(settings).String("disableAutoMode") == "disable") {
			reason = "auto mode disabled by settings"
		} else if s.autoEnabledState() == "disabled" {
			reason = "auto mode is unavailable for your plan"
		}
		if reason != "" {
			if s.options.Environment("USER_TYPE") == "ant" {
				reason += " · #claude-code-feedback"
			}
			return "Cannot set permission mode to auto: " + reason, nil
		}
	}
	return "", nil
}
