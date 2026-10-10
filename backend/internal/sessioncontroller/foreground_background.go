package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The actual runtime promotion callback belongs to the same task entry as
// cancellation/output/join. No parallel foreground-agent registry is created.
func (c *Controller) BindForegroundBackgroundOnOwner(scope *cooperative.Scope, id string, promote func(*cooperative.Scope) error) error {
	root, e := c.todosController(scope)
	if e != nil {
		return e
	}
	if task := root.appState.Tasks[id]; task != nil {
		task.Promote = promote
	}
	root.publishForegroundTasks()
	return nil
}
func (c *Controller) HasForegroundAgentTasksOnOwner(scope *cooperative.Scope) (bool, error) {
	root, e := c.todosController(scope)
	if e != nil {
		return false, e
	}
	for _, task := range root.appState.Tasks {
		if task.Data.String("type") == "local_agent" && string(task.Data.Get("isBackgrounded")) != "true" && task.Promote != nil {
			return true, nil
		}
	}
	return false, nil
}
func (c *Controller) BackgroundForegroundAgentsOnOwner(scope *cooperative.Scope) (bool, error) {
	root, e := c.todosController(scope)
	if e != nil {
		return false, e
	}
	rows, e := root.BackgroundTasksOnOwner(scope)
	if e != nil {
		return false, e
	}
	changed := false
	for _, data := range rows {
		task := root.appState.Tasks[data.String("id")]
		if task != nil && task.Data.String("type") == "local_agent" && string(task.Data.Get("isBackgrounded")) != "true" && task.Promote != nil {
			if e := task.Promote(scope); e != nil {
				return changed, e
			}
			changed = true
		}
	}
	return changed, nil
}
func MarkForegroundAgentBackground(task protocol.JSONObject) protocol.JSONObject {
	copy := task.Clone()
	copy.Set("isBackgrounded", raw(true))
	return copy
}

// Source hasForegroundTasks excludes the main-session Agent, while ordinary
// shell and subagent runtime handles remain eligible until they unregister.
func sourceForegroundTask(task *BackgroundTask) bool {
	if task == nil || string(task.Data.Get("isBackgrounded")) == "true" || task.Promote == nil {
		return false
	}
	switch task.Data.String("type") {
	case "local_bash":
		return true
	case "local_agent":
		return task.Data.String("agentType") != "main-session"
	}
	return false
}
func (c *Controller) HasForegroundTasksOnOwner(scope *cooperative.Scope) (bool, error) {
	root, e := c.todosController(scope)
	if e != nil {
		return false, e
	}
	for _, task := range root.appState.Tasks {
		if sourceForegroundTask(task) {
			return true, nil
		}
	}
	return false, nil
}
func (c *Controller) BackgroundForegroundTasksOnOwner(scope *cooperative.Scope) (bool, error) {
	root, e := c.todosController(scope)
	if e != nil {
		return false, e
	}
	rows, e := root.BackgroundTasksOnOwner(scope)
	if e != nil {
		return false, e
	}
	changed := false
	// Source backgrounds every shell first, then every local Agent, preserving
	// registration order within each group and the single task map throughout.
	for _, kind := range []string{"local_bash", "local_agent"} {
		for _, row := range rows {
			id := row.String("id")
			task := root.appState.Tasks[id]
			if task == nil || task.Data.String("type") != kind || string(task.Data.Get("isBackgrounded")) == "true" || task.Promote == nil {
				continue
			}
			if e := task.Promote(scope); e != nil {
				return changed, e
			}
			if current := root.appState.Tasks[id]; current != nil && string(current.Data.Get("isBackgrounded")) == "true" {
				changed = true
			}
		}
	}
	return changed, nil
}
