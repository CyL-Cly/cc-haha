package sdkpermissions

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// CCR externalMetadataToAppState assigns only the restored mode. It does not
// invoke an SDK entry guard or recreate rules/prePlanMode/classifier state.
func (s *State) RestoreExternalMode(scope *cooperative.Scope, mode string) error {
	if s == nil || !scope.BelongsTo(s.owner) || !s.nativeProcess {
		return cooperative.ErrRestricted
	}
	switch mode {
	case "default", "plan", "acceptEdits", "bypassPermissions", "dontAsk", "auto":
	default:
		mode = "default"
	}
	s.context.Set("mode", quote(mode))
	return nil
}
