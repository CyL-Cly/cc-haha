package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync/atomic"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// BackgroundTask is one actual Source AppState.tasks entry. Runtime cancellation
// and output readers belong to that entry; there is no second global task map.
// Only the owning Controller's owner may read or mutate its live state.
type BackgroundTask struct {
	Data             protocol.JSONObject
	Cancel           context.CancelCauseFunc
	WorkCancel       context.CancelCauseFunc
	IdleCallbacks    []func()
	ReadOutput       func(context.Context) (json.RawMessage, error)
	ReadActiveOutput func(context.Context) (json.RawMessage, error)
	ReadDelta        func(context.Context, int64) (bool, int64, error)
	Join             func(context.Context) error
	Promote          func(*cooperative.Scope) error
	// Stop is the original task-specific workflow kill path. It settles that
	// same AppState entry, cancels its agents and emits its own notification.
	Stop            func(*cooperative.Scope, float64) error
	WorkflowControl func(*cooperative.Scope, string, string) error
	order           uint64
}
type StopBackgroundTaskError struct{ Code, Message string }

func (e *StopBackgroundTaskError) Error() string                    { return e.Message }
func cloneBackgroundData(v protocol.JSONObject) protocol.JSONObject { return object(raw(v)) }
func (c *Controller) RegisterBackgroundTaskOnOwner(scope *cooperative.Scope, task *BackgroundTask) error {
	root, e := c.todosController(scope)
	if e != nil {
		return e
	}
	if task == nil || !task.Data.IsObject() || text(task.Data.Get("id")) == "" || text(task.Data.Get("type")) == "" || task.Cancel == nil || task.ReadOutput == nil || task.Join == nil {
		return errors.New("Background task registration requires actual identity, cancellation and output")
	}
	if root.appState.Tasks == nil {
		root.appState.Tasks = map[string]*BackgroundTask{}
	}
	id := text(task.Data.Get("id"))
	previous := root.appState.Tasks[id]
	var order uint64
	for _, old := range root.appState.Tasks {
		if old.order >= order {
			order = old.order + 1
		}
	}
	data := cloneBackgroundData(task.Data)
	if previous != nil {
		data = MergeAgentReplacement(previous.Data, data)
		order = previous.order
	}
	root.appState.Tasks[id] = &BackgroundTask{Data: data, Cancel: task.Cancel, WorkCancel: task.WorkCancel, IdleCallbacks: append([]func(){}, task.IdleCallbacks...), ReadOutput: task.ReadOutput, ReadActiveOutput: task.ReadActiveOutput, ReadDelta: task.ReadDelta, Join: task.Join, Promote: task.Promote, Stop: task.Stop, WorkflowControl: task.WorkflowControl, order: order}
	root.publishForegroundTasks()
	return nil
}

// BackgroundTaskRegistration is an ephemeral startup receipt. Rollback must run
// on the same actual task Owner; it is never stored in AppState or on disk.
type BackgroundTaskRegistration struct {
	Replaced bool
	// Commit only releases startup custody; it never reads or mutates AppState.
	Commit   func()
	Rollback func(*cooperative.Scope) error
}

// RegisterAgentTaskOnOwner retains the normal task -> name publication order,
// while preserving the exact overwritten entry for a failed startup. Registering
// that entry again would run MergeAgentReplacement and lose the prior state.
func (c *Controller) RegisterAgentTaskOnOwner(scope *cooperative.Scope, task *BackgroundTask, name string) (*BackgroundTaskRegistration, error) {
	root, e := c.todosController(scope)
	if e != nil {
		return nil, e
	}
	if task == nil {
		return nil, errors.New("Agent task registration requires an actual task")
	}
	id := task.Data.String("id")
	previous := root.appState.Tasks[id]
	oldName, nameExisted := root.appState.AgentNameRegistry[name]
	if e = c.RegisterBackgroundTaskOnOwner(scope, task); e != nil {
		return nil, e
	}
	admitted := root.appState.Tasks[id]
	owner := c.options.Bootstrap.Owner()
	var pending atomic.Bool
	pending.Store(true)
	registration := &BackgroundTaskRegistration{Replaced: previous != nil, Commit: func() { pending.Store(false) }}
	registration.Rollback = func(scope *cooperative.Scope) error {
		// Like timer finally-cleanup, this already-authorized receipt may undo
		// its exact registration after the caller's context is cancelled. It
		// cannot grant new business access or revive a retired Controller.
		if !scope.BelongsTo(owner) || !c.active || !root.active || !root.BootstrapOwns(owner) {
			return cooperative.ErrRestricted
		}
		if !pending.CompareAndSwap(true, false) {
			return nil
		}
		// An independently replaced/deleted task owns its subsequent state.
		if root.appState.Tasks[id] != admitted {
			return nil
		}
		if previous == nil {
			delete(root.appState.Tasks, id)
		} else {
			root.appState.Tasks[id] = previous
		}
		if name != "" && root.appState.AgentNameRegistry[name] == id {
			if nameExisted {
				root.appState.AgentNameRegistry[name] = oldName
			} else {
				delete(root.appState.AgentNameRegistry, name)
			}
		}
		root.publishForegroundTasks()
		return nil
	}
	if name != "" {
		if e = c.RegisterAgentNameOnOwner(scope, name, id); e != nil {
			return nil, errors.Join(e, registration.Rollback(scope))
		}
	}
	return registration, nil
}

func (c *Controller) BackgroundTaskOnOwner(scope *cooperative.Scope, id string) (*BackgroundTask, error) {
	root, e := c.todosController(scope)
	if e != nil {
		return nil, e
	}
	task := root.appState.Tasks[id]
	if task == nil {
		return nil, nil
	}
	return &BackgroundTask{Data: cloneBackgroundData(task.Data), Cancel: task.Cancel, WorkCancel: task.WorkCancel, IdleCallbacks: append([]func(){}, task.IdleCallbacks...), ReadOutput: task.ReadOutput, ReadActiveOutput: task.ReadActiveOutput, ReadDelta: task.ReadDelta, Join: task.Join, Promote: task.Promote, Stop: task.Stop, WorkflowControl: task.WorkflowControl, order: task.order}, nil
}
func (c *Controller) BackgroundTasksOnOwner(scope *cooperative.Scope) ([]protocol.JSONObject, error) {
	root, e := c.todosController(scope)
	if e != nil {
		return nil, e
	}
	out := []protocol.JSONObject{}
	ordered := make([]*BackgroundTask, 0, len(root.appState.Tasks))
	for _, task := range root.appState.Tasks {
		ordered = append(ordered, task)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].order < ordered[j].order })
	for _, task := range ordered {
		out = append(out, cloneBackgroundData(task.Data))
	}
	return out, nil
}
func (c *Controller) UpdateBackgroundTaskOnOwner(scope *cooperative.Scope, id string, update func(protocol.JSONObject) (protocol.JSONObject, error)) error {
	root, e := c.todosController(scope)
	if e != nil {
		return e
	}
	if task := root.appState.Tasks[id]; task != nil {
		value, e := update(cloneBackgroundData(task.Data))
		if e != nil {
			return e
		}
		task.Data = cloneBackgroundData(value)
		root.publishForegroundTasks()
	}
	return nil
}
func (c *Controller) MarkBackgroundNotifiedOnOwner(scope *cooperative.Scope, id string) error {
	return c.UpdateBackgroundTaskOnOwner(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
		task.Set("notified", raw(true))
		return task, nil
	})
}
func (c *Controller) DeleteBackgroundTaskOnOwner(scope *cooperative.Scope, id string) error {
	root, e := c.todosController(scope)
	if e != nil {
		return e
	}
	delete(root.appState.Tasks, id)
	root.publishForegroundTasks()
	return nil
}
func (c *Controller) StopBackgroundTaskOnOwner(scope *cooperative.Scope, id string, now float64) (protocol.JSONObject, error) {
	root, e := c.todosController(scope)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	task := root.appState.Tasks[id]
	if task == nil {
		return protocol.JSONObject{}, &StopBackgroundTaskError{"not_found", "No task found with ID: " + id}
	}
	status := text(task.Data.Get("status"))
	if status != "running" {
		return protocol.JSONObject{}, &StopBackgroundTaskError{"not_running", "Task " + id + " is not running (status: " + status + ")"}
	}
	kind := text(task.Data.Get("type"))
	if (kind == "local_workflow" || kind == "remote_agent" && c.options.NativeTools) && task.Stop != nil {
		if e := task.Stop(scope, now); e != nil {
			return protocol.JSONObject{}, e
		}
		return protocol.NewJSONObject(protocol.JSONField{Name: "taskId", Value: quote(id)}, protocol.JSONField{Name: "taskType", Value: quote(kind)}, protocol.JSONField{Name: "command", Value: task.Data.Get("description")}), nil
	}
	if task.Cancel == nil || kind != "local_bash" && kind != "local_agent" {
		return protocol.JSONObject{}, &StopBackgroundTaskError{"unsupported_type", "Unsupported task type: " + kind}
	}
	task.Cancel(context.Canceled)
	updated := task.Data.Clone()
	updated.Set("status", quote("killed"))
	updated.Set("endTime", raw(now))
	if kind == "local_bash" {
		updated.Set("notified", raw(true))
	} else if kind == "local_agent" {
		updated.Delete("selectedAgent")
		if string(updated.Get("retain")) == "true" {
			updated.Delete("evictAfter")
		} else {
			updated.Set("evictAfter", raw(now+30000))
		}
	}
	task.Data = updated
	root.publishForegroundTasks()
	command := task.Data.Get("description")
	if kind == "local_bash" {
		command = task.Data.Get("command")
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "taskId", Value: quote(id)}, protocol.JSONField{Name: "taskType", Value: quote(kind)}, protocol.JSONField{Name: "command", Value: command}), nil
}
func (c *Controller) CancelBackgroundTasksOnOwner(scope *cooperative.Scope, now float64) error {
	tasks, e := c.BackgroundTasksOnOwner(scope)
	if e != nil {
		return e
	}
	for _, task := range tasks {
		if text(task.Get("status")) == "running" {
			if _, e = c.StopBackgroundTaskOnOwner(scope, text(task.Get("id")), now); e != nil {
				return e
			}
		}
	}
	return nil
}
