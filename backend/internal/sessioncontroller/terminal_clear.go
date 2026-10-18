package sessioncontroller

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type TerminalClearTasks struct {
	PreservedAgents map[string]bool
	RunningAgents   []protocol.JSONObject
	Removed         []protocol.JSONObject
}

// TerminalClearTasksOnOwner uses Source's exact partition: only an explicit
// isBackgrounded=false kills a task. Absent/null flags and finished background
// tasks survive with their existing cancellation/output ownership.
func (c *Controller) TerminalClearTasksOnOwner(scope *cooperative.Scope) (TerminalClearTasks, error) {
	out := TerminalClearTasks{PreservedAgents: map[string]bool{}}
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) || !c.options.PublicPrint || !c.options.NativeTools || !c.options.AsyncPersistence || c.options.WriterOptions.AgentID != "" || !c.active {
		return out, cooperative.ErrRestricted
	}
	for _, task := range c.appState.Tasks {
		if string(task.Data.Get("isBackgrounded")) == "false" {
			out.Removed = append(out.Removed, task.Data.Clone())
			continue
		}
		if task.Data.String("type") == "local_agent" {
			out.PreservedAgents[task.Data.String("agentId")] = true
			if task.Data.String("status") == "running" {
				out.RunningAgents = append(out.RunningAgents, task.Data.Clone())
			}
		} else if task.Data.String("type") == "in_process_teammate" {
			identity, _ := task.Data.Object("identity")
			out.PreservedAgents[identity.String("agentId")] = true
		}
	}
	return out, nil
}

func (c *Controller) ClearTerminalStateOnOwner(scope *cooperative.Scope, tasks TerminalClearTasks) error {
	if _, err := c.TerminalClearTasksOnOwner(scope); err != nil {
		return err
	}
	for id, task := range c.appState.Tasks {
		if string(task.Data.Get("isBackgrounded")) != "false" {
			continue
		}
		if task.Data.String("status") == "running" {
			if task.WorkCancel != nil {
				task.WorkCancel(context.Canceled)
			}
			if task.Cancel != nil {
				task.Cancel(context.Canceled)
			}
		}
		delete(c.appState.Tasks, id)
	}
	c.publishForegroundTasks()
	c.messages = nil
	c.readCache = newReadCache(c.readCache.maxEntries)
	c.appState.FileHistory = emptyAppState().FileHistory
	c.resume.ClearInvokedSkills(tasks.PreservedAgents)
	c.writer.mu.Lock()
	c.writer.metadata = protocol.NewJSONObject()
	c.writer.lastPrompt = nil
	c.writer.mu.Unlock()
	if c.writer.async != nil {
		if err := c.writer.async.ClearSessionUUIDCache(scope); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) RegenerateTerminalSessionOnOwner(scope *cooperative.Scope) (string, error) {
	if _, err := c.TerminalClearTasksOnOwner(scope); err != nil {
		return "", err
	}
	cache, err := c.options.Bootstrap.PlanSlugCache(scope)
	if err != nil {
		return "", err
	}
	if err = cache.Clear(scope); err != nil {
		return "", err
	}
	sid, err := c.options.Bootstrap.RegenerateSessionID(scope, true)
	if err != nil {
		return "", err
	}
	c.writer.Switch(sid, "")
	future, err := c.writer.BeginResetSessionFile(scope)
	if err != nil {
		return "", err
	}
	_, err = future.Await(scope)
	return sid, err
}
