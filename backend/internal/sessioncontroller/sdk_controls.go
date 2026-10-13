package sessioncontroller

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

func (c *Controller) SetSDKPermissionMode(scope *cooperative.Scope, mode string) (resultErr error) {
	if c == nil || c.options.Bootstrap == nil || c.appState.permission == nil {
		return cooperative.ErrRestricted
	}
	if err := c.options.Bootstrap.CheckScope(scope); err != nil {
		return err
	}
	was, e := c.appState.permission.AutoModeActive(scope)
	if e != nil {
		return e
	}
	before, e := c.appState.permission.PlanModeContext(scope)
	if e != nil {
		return e
	}
	defer func() {
		if resultErr == nil {
			resultErr = c.observePermissionMode(scope, before.String("mode"))
		}
	}()
	if e := c.appState.permission.SetMode(scope, mode); e != nil {
		return e
	}
	active, e := c.appState.permission.AutoModeActive(scope)
	if e != nil {
		return e
	}
	if was && !active {
		if e = c.options.Bootstrap.SetNeedsAutoModeExitAttachment(scope, true); e != nil {
			return e
		}
	}
	if e := c.options.Bootstrap.HandlePlanModeTransition(scope, before.String("mode"), mode); e != nil {
		return e
	}
	if before.String("mode") == "plan" && mode != "plan" {
		return c.options.Bootstrap.SetHasExitedPlanMode(scope, true)
	}
	return nil
}
