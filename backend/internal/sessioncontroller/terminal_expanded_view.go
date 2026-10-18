package sessioncontroller

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// This is the committed UI projection of the existing AppState field. Task
// tools and the terminal read/write the same value, without replacing AppState.
func (c *Controller) SetTerminalExpandedViewOnOwner(scope *cooperative.Scope, value string) error {
	if value != "none" && value != "tasks" && value != "teammates" {
		return cooperative.ErrRestricted
	}
	root, err := c.todosController(scope)
	if err != nil {
		return err
	}
	root.appState.ExpandedView = value
	return nil
}
