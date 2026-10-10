package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// InjectTeamModelBreadcrumbs is the original SDK set_model mutableMessages
// append before any worker task is released. It does not enqueue another ASK.
func (c *Controller) InjectTeamModelBreadcrumbs(scope *cooperative.Scope, requested, display string) ([]protocol.JSONObject, error) {
	team, _, _, err := c.TeamWorkerIdentity(scope)
	if err != nil || team == "" || !c.active {
		return nil, cooperative.ErrRestricted
	}
	return c.InjectSessionModelBreadcrumbs(scope, requested, display)
}
