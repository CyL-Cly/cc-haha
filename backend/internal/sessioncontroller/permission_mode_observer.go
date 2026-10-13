package sessioncontroller

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// One observer of the canonical permission context serves native SDK and CCR
// consumers. Mode changes originating in plans, settings or terminal controls
// follow the same path as set_permission_mode.
func (c *Controller) BindPermissionModeObserver(scope *cooperative.Scope, listener func(*cooperative.Scope, string, string) error) error {
	if _, err := c.PlanModeContext(scope); err != nil {
		return err
	}
	c.permissionModeChanged = listener
	return nil
}
func (c *Controller) observePermissionMode(scope *cooperative.Scope, previous string) error {
	if c.permissionModeChanged == nil {
		return nil
	}
	current, err := c.PlanModeContext(scope)
	if err != nil {
		return err
	}
	if previous == current.String("mode") {
		return nil
	}
	return c.permissionModeChanged(scope, previous, current.String("mode"))
}
