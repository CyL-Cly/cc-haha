package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (c *Controller) ApplySDKPermissionUpdates(scope *cooperative.Scope, updates []protocol.JSONObject, persist func(protocol.JSONObject) error) error {
	if c == nil || c.options.Bootstrap == nil || c.appState.permission == nil {
		return cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return e
	}
	return c.appState.permission.ApplyPermissionUpdates(scope, updates, func(mode string) error { return c.SetSDKPermissionMode(scope, mode) }, persist)
}
