package sessioncontroller

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// A borrowed view of the original queue command; publication does not dequeue it.
type taskNotificationIdentity struct{ generation uint64 }

type TaskNotificationCommand struct {
	Content       json.RawMessage
	Mode, AgentID string
}

func (c *Controller) BindTaskNotificationsOnOwner(scope *cooperative.Scope, publish func(*cooperative.Scope, TaskNotificationCommand) (bool, error)) (func(*cooperative.Scope) error, error) {
	if c == nil || c.options.Bootstrap == nil || c.options.InheritedPermissions != nil || publish == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := c.options.Bootstrap.CheckScope(scope); err != nil {
		return nil, err
	}
	if c.taskNotificationPublisher != nil {
		return nil, cooperative.ErrRestricted
	}
	c.taskNotificationGeneration++
	generation := c.taskNotificationGeneration
	c.taskNotificationPublisher = publish
	dispose := func(next *cooperative.Scope) error {
		if err := c.options.Bootstrap.CheckScope(next); err != nil {
			return err
		}
		if c.taskNotificationGeneration == generation {
			c.taskNotificationPublisher = nil
		}
		return nil
	}
	for i := range c.queue {
		if err := c.publishTaskNotificationOnOwner(scope, i); err != nil {
			_ = dispose(scope)
			return nil, err
		}
	}
	return dispose, nil
}
func (c *Controller) publishTaskNotificationOnOwner(scope *cooperative.Scope, index int) error {
	if c.queue[index].Mode != "task-notification" || c.queue[index].AgentID != "" || c.taskNotificationPublisher == nil {
		return nil
	}
	if err := c.options.Bootstrap.CheckScope(scope); err != nil {
		return err
	}
	if c.queue[index].taskNotificationIdentity == nil {
		c.queue[index].taskNotificationIdentity = &taskNotificationIdentity{}
	}
	identity := c.queue[index].taskNotificationIdentity
	if identity.generation == c.taskNotificationGeneration {
		return nil
	}
	command := c.queue[index]
	previous := identity.generation
	// Match the Source WeakSet insertion before enqueueing stdout. The identity
	// survives queue slicing and a subscriber's reentrant queue mutation.
	identity.generation = c.taskNotificationGeneration
	published, err := c.taskNotificationPublisher(scope, TaskNotificationCommand{Content: append(json.RawMessage(nil), command.Content...), Mode: command.Mode, AgentID: command.AgentID})
	if !published {
		identity.generation = previous
	}
	return err
}

// Once print owns root publication, producers must not fall back to emitting
// directly after its subscriber is disposed. Source unsubscribe suppresses
// late terminals while their model commands remain in the original queue.
func (c *Controller) RootTaskNotificationsUseQueueOnOwner(scope *cooperative.Scope) bool {
	return c != nil && c.options.Bootstrap != nil && c.options.Bootstrap.CheckScope(scope) == nil && c.taskNotificationGeneration > 0
}
