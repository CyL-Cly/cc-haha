package clibootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

// A desktop Stop keeps the Go owner available, unlike the Source's forced
// shared-process exit after unsupported in-process stop_task. End and join
// those child runtimes explicitly, preserving their resumable transcript,
// metadata, roster and process-local runner templates. Keep Source's failed
// runtime-exit bookend rather than reporting an unconfirmed successful task.
// The caller already holds the independent Cancel scope.
func (m *MainPrint) pauseNativeInProcessTeammatesOnOwner(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	rows, err := m.Controller.BackgroundTasksOnOwner(scope)
	if err != nil {
		return err
	}
	type stopped struct {
		row     protocol.JSONObject
		join    func(context.Context) error
		summary string
	}
	var stoppedTasks []stopped
	var failures []error
	for _, row := range rows {
		if row.String("type") != "in_process_teammate" || row.String("status") != "running" {
			continue
		}
		entry, err := m.Controller.BackgroundTaskOnOwner(scope, row.String("id"))
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if entry == nil || entry.Cancel == nil || entry.Join == nil {
			failures = append(failures, mainCap("actual in-process teammate Stop cancel and join"))
			continue
		}
		description := row.String("description")
		if value := row.Get("description"); value == nil || string(value) == "null" {
			description = row.String("id")
		}
		summary := description + ": runtime connection lost; task completion could not be confirmed"
		// Set the terminal guard before cancellation yields to the child. Its
		// finally block must not overwrite the bookend with completed or emit it
		// a second time. No KillTeammate roster deletion belongs in this path.
		if err := m.Controller.UpdateBackgroundTaskOnOwner(scope, row.String("id"), func(task protocol.JSONObject) (protocol.JSONObject, error) {
			task, _, err := teamspawn.FinishTask(task, "failed", m.options.Native.NowMillis(), summary)
			return task, err
		}); err != nil {
			failures = append(failures, err)
			continue
		}
		entry.Cancel(context.Canceled)
		stoppedTasks = append(stoppedTasks, stopped{row.Clone(), entry.Join, summary})
	}
	if len(stoppedTasks) == 0 {
		return errors.Join(failures...)
	}
	work, err := nativeSkillWork(m, scope, func() (struct{}, error) {
		var failed []error
		for _, task := range stoppedTasks {
			failed = append(failed, task.join(context.Background()))
		}
		return struct{}{}, errors.Join(failed...)
	})
	if err == nil {
		_, err = work.Await(scope)
	}
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	for _, task := range stoppedTasks {
		row := task.row
		identity, _ := row.Object("identity")
		event := protocol.NewJSONObject(
			protocol.JSONField{Name: "type", Value: quoteMain("system")},
			protocol.JSONField{Name: "subtype", Value: quoteMain("task_notification")},
			protocol.JSONField{Name: "task_id", Value: row.Get("id")},
			protocol.JSONField{Name: "tool_use_id", Value: row.Get("toolUseId")},
			protocol.JSONField{Name: "task_type", Value: quoteMain("in_process_teammate")},
			protocol.JSONField{Name: "description", Value: row.Get("description")},
			protocol.JSONField{Name: "owner_agent_id", Value: identity.Get("agentId")},
			protocol.JSONField{Name: "status", Value: quoteMain("failed")},
			protocol.JSONField{Name: "summary", Value: quoteMain(task.summary)},
			protocol.JSONField{Name: "timestamp", Value: quoteMain(time.UnixMilli(int64(m.options.Native.NowMillis())).UTC().Format("2006-01-02T15:04:05.000Z"))},
		)
		if row.String("description") == "" {
			event.Delete("description")
		}
		failures = append(failures, m.nativeBackgroundBindings().Emit(scope, event))
	}
	return errors.Join(failures...)
}
