package clibootstrap

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func (m *MainPrint) nativeBackgroundBindings() *builtintools.BackgroundTaskBindings {
	if d := m.options.DetachedAgent; d != nil {
		return d.bindTasks(m)
	}
	n := m.options.Native
	return &builtintools.BackgroundTaskBindings{RootNotificationsUseQueue: m.Controller.RootTaskNotificationsUseQueueOnOwner, RegisterName: m.Controller.RegisterAgentNameOnOwner, ResolveName: m.Controller.ResolveAgentNameOnOwner, NotifyForAgent: m.Controller.EnqueueTaskNotificationOnOwner, Register: m.Controller.RegisterBackgroundTaskOnOwner, Lookup: m.Controller.BackgroundTaskOnOwner, List: m.Controller.BackgroundTasksOnOwner, Update: m.Controller.UpdateBackgroundTaskOnOwner, MarkNotified: m.Controller.MarkBackgroundNotifiedOnOwner, Stop: m.Controller.StopBackgroundTaskOnOwner, Delete: m.Controller.DeleteBackgroundTaskOnOwner, OutputDirectory: m.nativeTaskOutputDirectory, AgentID: m.options.AgentID, CheckPath: func(path string) error { return n.check("task-output", path) }, Emit: func(scope *cooperative.Scope, event protocol.JSONObject) error {
		if !scope.BelongsTo(m.Owner) {
			return cooperative.ErrRestricted
		}
		event = event.Clone()
		event.Set("session_id", quoteMain(m.Controller.SessionID()))
		event.Set("uuid", quoteMain(uuidMain()))
		data, e := protocol.EncodeNDJSON(event)
		if e != nil {
			return e
		}
		return m.enqueue(data)
	}, Notify: func(scope *cooperative.Scope, _ string, message string) error {
		return m.Controller.EnqueueTaskNotificationOnOwner(scope, m.options.AgentID, message)
	}}
}
func (m *MainPrint) nativeTaskOutputDirectory(scope *cooperative.Scope) (string, error) {
	if !scope.BelongsTo(m.Owner) {
		return "", cooperative.ErrRestricted
	}
	if m.taskOutputDirectory != "" {
		return m.taskOutputDirectory, nil
	}
	if d := m.options.DetachedAgent; d != nil {
		m.taskOutputDirectory = d.TaskDirectory
		return d.TaskDirectory, nil
	}
	n := m.options.Native
	base := n.Environment["CLAUDE_CODE_TMPDIR"]
	if base == "" {
		base = "/tmp"
		if !n.NativeProcess {
			base = n.Temp
		}
	}
	if resolved, e := filepath.EvalSymlinks(base); e == nil {
		base = resolved
	}
	directoryName := "claude-" + strconv.Itoa(os.Getuid())
	if m.options.Platform == "windows" {
		directoryName = "claude"
	}
	m.taskOutputDirectory = filepath.Join(base, directoryName, projectpath.Sanitize(n.CWD), m.Controller.SessionID(), "tasks")
	return m.taskOutputDirectory, nil
}
func (m *MainPrint) nativeBackgroundSnapshot(scope *cooperative.Scope) ([]sdkhost.BackgroundTask, error) {
	tasks, e := m.nativeBackgroundBindings().List(scope)
	if e != nil {
		return nil, e
	}
	out := make([]sdkhost.BackgroundTask, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, sdkhost.BackgroundTask{Type: task.String("type"), Running: task.String("status") == "running", Background: task.String("type") == "local_workflow" || string(task.Get("isBackgrounded")) == "true"})
	}
	return out, nil
}
func (m *MainPrint) StopBackgroundTask(ctx context.Context, id string) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, e := m.Owner.Submit(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed || m.nativeTools == nil {
			return cooperative.ErrClosed
		}
		var e error
		result, e = m.nativeTools.StopBackgroundTaskFromControl(scope, id)
		return e
	})
	if e == nil {
		e = receipt.Wait()
	}
	return result, e
}

func (m *MainPrint) drainBackgroundTasksOnOwner(scope *cooperative.Scope, ctx context.Context) error {
	for {
		if context.Cause(ctx) != nil {
			return nil
		}
		consumed, e := m.Host.RunDequeued(scope, ctx)
		if e != nil {
			return e
		}
		if consumed {
			continue
		}
		tasks, e := m.nativeBackgroundSnapshot(scope)
		if e != nil {
			return e
		}
		running := false
		for _, task := range tasks {
			if task.Running && task.Background && task.Type != "in_process_teammate" {
				running = true
				break
			}
		}
		if !running {
			if e = m.Host.FlushHeldBackgroundResult(scope); e != nil {
				return e
			}
			if handled, e := m.notifyDurableLeaderInbox(scope); handled || e != nil {
				return e
			}
			poll, e := m.nativeLeaderInboxOnOwner(ctx, scope, false)
			if e != nil {
				return e
			}
			if poll {
				continue
			}
			return nil
		}
		wake := make(chan struct{})
		timer := time.AfterFunc(100*time.Millisecond, func() { close(wake) })
		e = scope.AwaitPromise(wake)
		timer.Stop()
		if e != nil {
			return e
		}
	}
}
