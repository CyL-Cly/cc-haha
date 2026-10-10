package sessioncontroller

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// Print's active drain polls the existing main-thread queue after asynchronous
// task completion. Idle enqueue_session_message keeps its start_if_idle gate;
// this observation is used only while that same print drain is active.
func (c *Controller) HasMainThreadQueuedCommandOnOwner(scope *cooperative.Scope) (bool, error) {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) || !c.active {
		return false, cooperative.ErrRestricted
	}
	for _, item := range c.queue {
		if item.AgentID == "" {
			return true, nil
		}
	}
	return false, nil
}
