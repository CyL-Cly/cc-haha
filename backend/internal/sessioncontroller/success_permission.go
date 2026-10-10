package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// SuccessPermissionOnOwner is query.ts's async AppState getter: it reads the
// same Controller state synchronously, then returns a fulfilled native Promise.
// Initialized permissions win. The legacy empty/default SDK profile uses the
// literal getDefaultAppState initial mode; teammates/plan and full aliases are
// not inferred. This method does not initialize a second permission authority.
func (c *Controller) SuccessPermissionOnOwner(scope *cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
	if c == nil || c.options.Bootstrap == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := c.options.Bootstrap.CheckScope(scope); err != nil {
		return nil, err
	}
	value := protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quote(c.appState.defaultPermissionMode)})
	if c.appState.permission != nil {
		_, actual, err := c.appState.permission.Read(scope)
		if err != nil {
			return nil, err
		}
		value = actual
	}
	future, err := ownerasync.NewOwnerFuture[protocol.JSONObject](c.options.Bootstrap.Owner())
	if err != nil {
		return nil, err
	}
	if err = future.Resolve(scope, value); err != nil {
		return nil, err
	}
	return future, nil
}
