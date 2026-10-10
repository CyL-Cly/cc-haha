package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// RegisterInProcessIdleOnOwner installs the original task's waitForIdle callback.
// A task already idle or terminal settles immediately, avoiding a lost wakeup.
func (c *Controller) RegisterInProcessIdleOnOwner(scope *cooperative.Scope, id string, wake func()) error {
	root, err := c.todosController(scope)
	if err != nil {
		return err
	}
	task := root.appState.Tasks[id]
	if wake == nil {
		return cooperative.ErrRestricted
	}
	if task == nil || task.Data.String("type") != "in_process_teammate" || task.Data.String("status") != "running" || string(task.Data.Get("isIdle")) == "true" {
		wake()
		return nil
	}
	task.IdleCallbacks = append(task.IdleCallbacks, wake)
	return nil
}

// ReleaseInProcessIdleOnOwner runs and clears only this actual task's waiters.
// It follows Source's synchronous onIdleCallbacks call before state publication.
func (c *Controller) ReleaseInProcessIdleOnOwner(scope *cooperative.Scope, id string) error {
	root, err := c.todosController(scope)
	if err != nil {
		return err
	}
	task := root.appState.Tasks[id]
	if task == nil || task.Data.String("type") != "in_process_teammate" {
		return nil
	}
	callbacks := task.IdleCallbacks
	task.IdleCallbacks = nil
	for _, wake := range callbacks {
		wake()
	}
	return nil
}

func (c *Controller) MarkInProcessIdleOnOwner(scope *cooperative.Scope, id string) (bool, error) {
	task, err := c.BackgroundTaskOnOwner(scope, id)
	if err != nil {
		return false, err
	}
	if task == nil || task.Data.String("type") != "in_process_teammate" {
		return false, cooperative.ErrRestricted
	}
	wasIdle := string(task.Data.Get("isIdle")) == "true"
	if err = c.ReleaseInProcessIdleOnOwner(scope, id); err != nil {
		return false, err
	}
	err = c.UpdateBackgroundTaskOnOwner(scope, id, func(data protocol.JSONObject) (protocol.JSONObject, error) {
		data.Set("isIdle", raw(true))
		data.Set("onIdleCallbacks", raw([]string{}))
		return data, nil
	})
	return wasIdle, err
}
