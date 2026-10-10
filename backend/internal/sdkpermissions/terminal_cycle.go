package sdkpermissions

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// NextTerminalMode is getNextPermissionMode's ordinary inactive-classifier branch.
// Active auto-classifier transitions remain owned by their existing guard;
// cached availability alone must not activate that unported live gate.
func NextTerminalMode(context protocol.JSONObject, userType string) string {
	switch context.String("mode") {
	case "default":
		if userType == "ant" {
			if string(context.Get("isBypassPermissionsModeAvailable")) == "true" {
				return "bypassPermissions"
			}
			return "default"
		}
		return "acceptEdits"
	case "acceptEdits":
		return "plan"
	case "plan":
		if string(context.Get("isBypassPermissionsModeAvailable")) == "true" {
			return "bypassPermissions"
		}
	}
	return "default"
}
