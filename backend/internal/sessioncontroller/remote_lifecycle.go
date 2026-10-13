package sessioncontroller

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// CompletedCommandLifecycle runs at the successful ASK consumer boundary, not
// when a model HTTP request finishes. Every merged input retains its own UUID.
func (c *Controller) CompletedCommandLifecycle(scope *cooperative.Scope) error {
	if c.commandLifecycle == nil {
		return nil
	}
	if c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return cooperative.ErrRestricted
	}
	ids := c.commandLifecycleIDs
	c.commandLifecycleIDs = nil
	if c.commandLifecycle != nil {
		for _, id := range ids {
			c.commandLifecycle(id, "completed")
		}
	}
	return nil
}
func (c *Controller) HistoricalCommandLifecycle(scope *cooperative.Scope, id json.RawMessage) error {
	if c.commandLifecycle == nil {
		return nil
	}
	if c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return cooperative.ErrRestricted
	}
	if c.commandLifecycle != nil {
		c.commandLifecycle(id, "completed")
	}
	return nil
}
