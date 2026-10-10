package sessioncontroller

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// Source getTaskListId uses team context first. This native branch is entered
// by an actual standalone main/child context; teammate producers stay separate.
func (c *Controller) TaskListIDOnOwner(scope *cooperative.Scope, agentID string) (string, error) {
	if _, err := c.todosController(scope); err != nil {
		return "", err
	}
	teamList, workerID, err := c.teamTaskList(scope)
	if err != nil {
		return "", err
	}
	expectedID := c.options.WriterOptions.AgentID
	if workerID != "" {
		expectedID = workerID
	}
	if agentID != expectedID {
		return "", cooperative.ErrRestricted
	}
	if teamList != "" {
		return teamList, nil
	}
	if id := c.options.ResumeOptions.Environment["CLAUDE_CODE_TASK_LIST_ID"]; id != "" {
		return id, nil
	}
	if agentID != "" {
		return agentID, nil
	}
	return c.SessionID(), nil
}

func (c *Controller) ExpandTaskViewOnOwner(scope *cooperative.Scope) error {
	root, err := c.todosController(scope)
	if err != nil {
		return err
	}
	root.appState.ExpandedView = "tasks"
	return nil
}

func (c *Controller) ExpandedViewOnOwner(scope *cooperative.Scope) (string, error) {
	root, err := c.todosController(scope)
	if err != nil {
		return "", err
	}
	return root.appState.ExpandedView, nil
}
