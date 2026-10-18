package clibootstrap

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sandboxruntime"
)

// TerminalPermissionDebugSnapshot observes the same live permission context
// and sandbox availability as the real tool host. It does not evaluate another
// permission decision, create a logger, or execute a model request.
func (m *MainPrint) TerminalPermissionDebugSnapshot(scope *cooperative.Scope) (protocol.JSONObject, bool, bool, error) {
	if m == nil || scope == nil || !scope.BelongsTo(m.Owner) || m.options.Native == nil || !m.options.Native.NativeProcess || m.Host == nil {
		return protocol.JSONObject{}, false, false, cooperative.ErrRestricted
	}
	permissions, err := m.Host.NativePermissionContext(scope)
	if err != nil {
		return protocol.JSONObject{}, false, false, err
	}
	hint := false
	parsed := cliargs.Parse(m.options.Arguments.DebugArguments, cliargs.Config{Version: m.options.Version})
	if parsed.Parsed != nil {
		for _, key := range []string{"debug", "debugToStderr"} {
			value, _ := json.Marshal(parsed.Parsed.Globals[key])
			hint = hint || terminalPermissionDebugTruthy(value)
		}
	}
	autoAllow := false
	if state := m.sandbox; state != nil {
		settings, err := m.Startup.GetInitialSettings()
		if err != nil {
			return protocol.JSONObject{}, false, false, err
		}
		sandbox, _ := settings.Object("sandbox")
		availability := sandboxruntime.ResolveAvailability(sandboxruntime.AvailabilityInput{Settings: *settings, InitialSettings: state.initial, Platform: state.platform, Supported: state.supported, DependencyErrors: state.dependencies})
		autoAllow = availability.Enabled && string(sandbox.Get("autoAllowBashIfSandboxed")) != "false"
	}
	return permissions.Clone(), hint, autoAllow, nil
}

func terminalPermissionDebugTruthy(value json.RawMessage) bool {
	if len(value) == 0 || string(value) == "null" || string(value) == "false" {
		return false
	}
	if value[0] == '"' {
		units, err := protocol.DecodeJavaScriptJSONString(value)
		return err == nil && len(units) > 0
	}
	if value[0] == '{' || value[0] == '[' || string(value) == "true" {
		return true
	}
	var number float64
	return json.Unmarshal(value, &number) == nil && number != 0
}
