package sdkpermissions

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// Cached UI availability and the current live gate must both permit Auto.
func NextTerminalMode(context protocol.JSONObject, userType string, liveAutoGate ...bool) string {
	auto := len(liveAutoGate) > 0 && liveAutoGate[0] && string(context.Get("isAutoModeAvailable")) == "true"
	switch context.String("mode") {
	case "default":
		if userType == "ant" {
			if string(context.Get("isBypassPermissionsModeAvailable")) == "true" {
				return "bypassPermissions"
			}
			if auto {
				return "auto"
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
		if auto {
			return "auto"
		}
	case "bypassPermissions":
		if auto {
			return "auto"
		}
	}
	return "default"
}
