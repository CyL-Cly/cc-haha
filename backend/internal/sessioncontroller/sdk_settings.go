package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

// UsesSettingsState only compares immutable persistence bindings.
func (c *Controller) UsesSettingsState(state *clisettings.State) bool {
	return c != nil && state != nil && c.options.WriterOptions.SettingsState == state && c.options.ResumeOptions.SettingsState == state
}
func (c *Controller) InitializeSDKSettings(scope *cooperative.Scope, state *sdkpermissions.State) error {
	if c == nil || c.options.Bootstrap == nil || !state.Owns(c.options.Bootstrap.Owner()) {
		return cooperative.ErrRestricted
	}
	if err := c.options.Bootstrap.CheckScope(scope); err != nil {
		return err
	}
	if c.newSDKAdmitted || c.appState.permission != nil {
		return capability("repeated/late SDK permission initialization")
	}
	settings, _, err := state.Read(scope)
	if err != nil {
		return err
	}
	c.appState.settings = settings
	c.appState.permission = state
	if parent := c.options.InheritedPermissions; parent != nil {
		if !parent.BootstrapOwns(c.options.Bootstrap.Owner()) || parent.appState.permission == nil {
			return cooperative.ErrRestricted
		}
		c.appState.permission = parent.appState.permission
	}
	return nil
}

// SDKSettingsOnOwner is the live AppState consumer used at ASK entry. Settings
// retain the cache reference, while the finite permission context is copied.
func (c *Controller) SDKSettingsOnOwner(scope *cooperative.Scope) (*protocol.JSONObject, protocol.JSONObject, error) {
	if c == nil || c.options.Bootstrap == nil {
		return nil, protocol.JSONObject{}, cooperative.ErrRestricted
	}
	if err := c.options.Bootstrap.CheckScope(scope); err != nil {
		return nil, protocol.JSONObject{}, err
	}
	if c.appState.permission == nil {
		return nil, protocol.JSONObject{}, capability("uninitialized SDK settings/permissions")
	}
	settings, permission, err := c.appState.permission.Read(scope)
	if err != nil {
		return settings, permission, err
	}
	if c.options.LiveSDKSettings != nil {
		settings, permission, err = c.options.LiveSDKSettings(scope)
		if err != nil {
			return settings, permission, err
		}
	}
	// Source's agentGetAppState reads the parent on every call. The overlay
	// changes a returned copy, preserving shared updates and permission identity.
	permission, err = c.ApplyAgentPermissionOverlayOnOwner(scope, permission)
	return settings, permission, err
}

func (c *Controller) ApplyAgentPermissionOverlayOnOwner(scope *cooperative.Scope, permission protocol.JSONObject) (protocol.JSONObject, error) {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	if override := c.options.AgentPermissionMode; override != "" && permission.String("mode") != "bypassPermissions" && permission.String("mode") != "acceptEdits" && permission.String("mode") != "auto" {
		permission = permission.Clone()
		permission.Set("mode", quote(override))
	}
	return permission, nil
}

// UsesOriginalSettingsContext denies a second settings producer/cache authority.
func (c *Controller) UsesOriginalSettingsContext(startup *clistartupenv.Service, settings *clisettings.Service, state *clisettings.State, environment *clistartupenv.Environment) bool {
	return c != nil && c.options.PublicResumeStartup == startup && c.options.UserContext.SharesSettings(settings, state, environment) && c.UsesSettingsState(state)
}

func (c *Controller) ReloadSDKSettings(scope *cooperative.Scope, settings *protocol.JSONObject, rules map[string]sdkpermissions.RuleSet) error {
	if c == nil || c.appState.permission == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return cooperative.ErrRestricted
	}
	if err := c.appState.permission.ReloadSettings(scope, settings, rules); err != nil {
		return err
	}
	c.appState.settings = settings
	return nil
}
