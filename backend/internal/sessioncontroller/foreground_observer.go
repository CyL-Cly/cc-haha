package sessioncontroller

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

type foregroundObserver struct {
	callback func(bool)
	previous bool
}

// This registry contains UI subscriptions only. Every published boolean is
// derived from the one actual AppState task map and its live runtime callback.
func (c *Controller) publishForegroundTasks() {
	c.publishBackgroundObservers()
	has := false
	for _, task := range c.appState.Tasks {
		if sourceForegroundTask(task) {
			has = true
			break
		}
	}
	for _, observer := range c.foregroundObservers {
		if observer.previous != has {
			observer.previous = has
			observer.callback(has)
		}
	}
}
func (c *Controller) SubscribeForegroundTasksOnOwner(scope *cooperative.Scope, callback func(bool)) (bool, func(*cooperative.Scope) error, error) {
	root, e := c.todosController(scope)
	if e != nil {
		return false, nil, e
	}
	if callback == nil {
		return false, nil, cooperative.ErrRestricted
	}
	has, e := root.HasForegroundTasksOnOwner(scope)
	if e != nil {
		return false, nil, e
	}
	if root.foregroundObservers == nil {
		root.foregroundObservers = map[uint64]*foregroundObserver{}
	}
	root.foregroundObserverSequence++
	id := root.foregroundObserverSequence
	root.foregroundObservers[id] = &foregroundObserver{callback, has}
	return has, func(scope *cooperative.Scope) error {
		owner, e := root.todosController(scope)
		if e != nil || owner != root {
			return cooperative.ErrRestricted
		}
		delete(root.foregroundObservers, id)
		return nil
	}, nil
}
