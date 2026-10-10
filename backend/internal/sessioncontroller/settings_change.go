package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

func (c *Controller) ApplyNativeSettingsChange(scope *cooperative.Scope, settings *protocol.JSONObject, rules map[string]sdkpermissions.RuleSet) error {
	if c == nil || c.appState.permission == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return cooperative.ErrRestricted
	}
	was, e := c.appState.permission.AutoModeActive(scope)
	if e != nil {
		return e
	}
	if err := c.appState.permission.ApplyNativeSettingsChange(scope, settings, rules); err != nil {
		return err
	}
	active, e := c.appState.permission.AutoModeActive(scope)
	if e != nil {
		return e
	}
	if active != was {
		if e = c.options.Bootstrap.SetNeedsAutoModeExitAttachment(scope, !active); e != nil {
			return e
		}
	}
	c.appState.settings = settings
	return nil
}
