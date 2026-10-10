package sessioncontroller

import (
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// UI subscribers borrow snapshots of the one task store. Runtime cancellation,
// join, leases and mutable task values never leave the controller owner.
func (c *Controller) backgroundTaskSnapshot() protocol.JSONObject {
	tasks := make([]*BackgroundTask, 0, len(c.appState.Tasks))
	for _, task := range c.appState.Tasks {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].order < tasks[j].order })
	value := protocol.NewJSONObject()
	for _, task := range tasks {
		value.Set(task.Data.String("id"), raw(cloneBackgroundData(task.Data)))
	}
	return value
}
func (c *Controller) publishBackgroundObservers() {
	for _, callback := range c.backgroundObservers {
		callback(c.backgroundTaskSnapshot())
	}
}
func (c *Controller) SubscribeBackgroundTasksOnOwner(scope *cooperative.Scope, callback func(protocol.JSONObject)) (protocol.JSONObject, func(*cooperative.Scope) error, error) {
	root, err := c.todosController(scope)
	if err != nil {
		return protocol.JSONObject{}, nil, err
	}
	if callback == nil {
		return protocol.JSONObject{}, nil, cooperative.ErrRestricted
	}
	if root.backgroundObservers == nil {
		root.backgroundObservers = map[uint64]func(protocol.JSONObject){}
	}
	root.backgroundObserverSequence++
	id := root.backgroundObserverSequence
	root.backgroundObservers[id] = callback
	return root.backgroundTaskSnapshot(), func(next *cooperative.Scope) error {
		owner, err := root.todosController(next)
		if err != nil || owner != root {
			return cooperative.ErrRestricted
		}
		delete(root.backgroundObservers, id)
		return nil
	}, nil
}
