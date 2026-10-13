package sessioncontroller

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (c *Controller) PlanModeContext(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if c == nil || c.options.Bootstrap == nil || c.appState.permission == nil {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return protocol.JSONObject{}, e
	}
	return c.appState.permission.PlanModeContext(scope)
}
func (c *Controller) EnterPlanMode(scope *cooperative.Scope) (resultErr error) {
	before, e := c.PlanModeContext(scope)
	if e != nil {
		return e
	}
	defer func() {
		if resultErr == nil {
			resultErr = c.observePermissionMode(scope, before.String("mode"))
		}
	}()
	wasAuto, e := c.appState.permission.AutoModeActive(scope)
	if e != nil {
		return e
	}
	if e := c.appState.permission.EnterPlanMode(scope); e != nil {
		return e
	}
	active, e := c.appState.permission.AutoModeActive(scope)
	if e != nil {
		return e
	}
	if wasAuto && !active {
		if e = c.options.Bootstrap.SetNeedsAutoModeExitAttachment(scope, true); e != nil {
			return e
		}
	}
	return c.options.Bootstrap.HandlePlanModeTransition(scope, before.String("mode"), "plan")
}
func (c *Controller) ExitPlanMode(scope *cooperative.Scope) (resultErr error) {
	before, e := c.PlanModeContext(scope)
	if e != nil {
		return e
	}
	defer func() {
		if resultErr == nil {
			resultErr = c.observePermissionMode(scope, before.String("mode"))
		}
	}()
	wasAuto, e := c.appState.permission.AutoModeActive(scope)
	if e != nil {
		return e
	}
	changed, e := c.appState.permission.ExitPlanMode(scope)
	if e != nil || !changed {
		return e
	}
	active, e := c.appState.permission.AutoModeActive(scope)
	if e != nil {
		return e
	}
	if wasAuto && !active {
		if e = c.options.Bootstrap.SetNeedsAutoModeExitAttachment(scope, true); e != nil {
			return e
		}
	}
	if e := c.options.Bootstrap.SetHasExitedPlanMode(scope, true); e != nil {
		return e
	}
	return c.options.Bootstrap.SetNeedsPlanModeExitAttachment(scope, true)
}

func (c *Controller) NativeAutoModeActive(scope *cooperative.Scope) (bool, error) {
	if c == nil || c.appState.permission == nil || c.options.Bootstrap == nil {
		return false, cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return false, e
	}
	return c.appState.permission.AutoModeActive(scope)
}

func (c *Controller) NativeClassifierClaudeMD(scope *cooperative.Scope) (json.RawMessage, error) {
	if c == nil || c.options.Bootstrap == nil || c.options.UserContext == nil {
		return nil, cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return nil, e
	}
	return c.options.UserContext.CachedClaudeMd(), nil
}
