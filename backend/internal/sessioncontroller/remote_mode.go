package sessioncontroller

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

func (c *Controller) RestoreRemotePermissionMode(scope *cooperative.Scope, mode string) error {
	before, err := c.PlanModeContext(scope)
	if err != nil {
		return err
	}
	if err = c.appState.permission.RestoreExternalMode(scope, mode); err != nil {
		return err
	}
	return c.observePermissionMode(scope, before.String("mode"))
}
