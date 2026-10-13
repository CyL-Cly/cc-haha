package sessioncontroller

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

func (c *Controller) NextTerminalPermissionMode(scope *cooperative.Scope, userType string) (string, error) {
	if _, err := c.PlanModeContext(scope); err != nil {
		return "", err
	}
	return c.appState.permission.NextTerminalMode(scope, userType)
}
func (c *Controller) NativeAutoModeOptIn(scope *cooperative.Scope) (bool, error) {
	if _, err := c.PlanModeContext(scope); err != nil {
		return false, err
	}
	return c.appState.permission.AutoModeOptIn(scope)
}
func (c *Controller) VerifyNativeAutoGate(scope *cooperative.Scope, cliFlag bool) (notice string, resultErr error) {
	previous, err := c.PlanModeContext(scope)
	if err != nil {
		return "", err
	}
	defer func() {
		if resultErr == nil {
			resultErr = c.observePermissionMode(scope, previous.String("mode"))
		}
	}()
	fast := c.appState.typedModel != nil && c.appState.typedModel.FastMode
	notice, exited, err := c.appState.permission.VerifyAutoModeGate(scope, cliFlag, fast)
	if err == nil && exited {
		err = c.options.Bootstrap.SetNeedsAutoModeExitAttachment(scope, true)
	}
	return notice, err
}

func (c *Controller) DeclineNativeAutoMode(scope *cooperative.Scope) (resultErr error) {
	previous, err := c.PlanModeContext(scope)
	if err != nil {
		return err
	}
	defer func() {
		if resultErr == nil {
			resultErr = c.observePermissionMode(scope, previous.String("mode"))
		}
	}()
	return c.appState.permission.DeclineAutoMode(scope)
}
