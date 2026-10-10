package sessioncontroller

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

func (c *Controller) SetInProcessWorkCancelOnOwner(scope *cooperative.Scope, id string, cancel context.CancelCauseFunc) error {
	root, err := c.todosController(scope)
	if err != nil {
		return err
	}
	if task := root.appState.Tasks[id]; task != nil && task.Data.String("type") == "in_process_teammate" {
		task.WorkCancel = cancel
		return nil
	}
	return cooperative.ErrRestricted
}

func (c *Controller) InterruptInProcessWorkOnOwner(scope *cooperative.Scope, id string) (bool, error) {
	root, err := c.todosController(scope)
	if err != nil {
		return false, err
	}
	task := root.appState.Tasks[id]
	if task == nil || task.Data.String("type") != "in_process_teammate" || task.Data.String("status") != "running" || task.WorkCancel == nil {
		return false, nil
	}
	task.WorkCancel(context.Canceled)
	return true, nil
}

func (c *Controller) InjectInProcessUserMessageOnOwner(scope *cooperative.Scope, id, message string) error {
	root, err := c.todosController(scope)
	if err != nil {
		return err
	}
	task := root.appState.Tasks[id]
	if task == nil || task.Data.String("type") != "in_process_teammate" {
		return nil
	}
	switch task.Data.String("status") {
	case "completed", "failed", "killed":
		return nil
	}
	return root.UpdateBackgroundTaskOnOwner(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
		var pending []string
		if err := json.Unmarshal(task.Get("pendingUserMessages"), &pending); err != nil {
			return task, err
		}
		task.Set("pendingUserMessages", raw(append(pending, message)))
		user := messages.OrderedUser(root.options.Factory.CreateUser(storage.Object{"content": quote(message)}))
		return teamspawn.AppendMessage(task, user)
	})
}

func (c *Controller) SetInProcessPermissionModeOnOwner(scope *cooperative.Scope, id, mode string) error {
	root, err := c.todosController(scope)
	if err != nil {
		return err
	}
	task := root.appState.Tasks[id]
	if task == nil || task.Data.String("type") != "in_process_teammate" {
		return cooperative.ErrRestricted
	}
	switch mode {
	case "default", "acceptEdits", "plan", "bypassPermissions":
	default:
		return cooperative.ErrRestricted
	}
	return root.UpdateBackgroundTaskOnOwner(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
		task.Set("permissionMode", quote(mode))
		return task, nil
	})
}
