package clibootstrap

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"os"
	"time"
)

// KillTeammate is the original task-specific TUI kill path. TaskStop retains
// Source's unsupported-type error for in_process_teammate.
func (m *MainPrint) KillTeammate(ctx context.Context, id string) (bool, error) {
	return inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (bool, error) {
		task, err := m.Controller.BackgroundTaskOnOwner(scope, id)
		if err != nil {
			return false, err
		}
		if task == nil || task.Data.String("type") != "in_process_teammate" || task.Data.String("status") != "running" {
			return false, nil
		}
		if task.Stop == nil {
			return false, mainCap("actual in-process teammate kill lifecycle")
		}
		return true, task.Stop(scope, m.options.Native.NowMillis())
	})
}

func (m *MainPrint) killNativeTeammateOnOwner(scope *cooperative.Scope, id string, now float64, unregister runtimecleanup.Unregister) error {
	if !scope.BelongsTo(m.Owner) || m.teamLeader == nil {
		return cooperative.ErrRestricted
	}
	task, err := m.Controller.BackgroundTaskOnOwner(scope, id)
	if err != nil {
		return err
	}
	if task == nil || task.Data.String("type") != "in_process_teammate" || task.Data.String("status") != "running" {
		return nil
	}
	identity, _ := task.Data.Object("identity")
	agentID, team := identity.String("agentId"), identity.String("teamName")
	if task.Cancel != nil {
		task.Cancel(context.Canceled)
	}
	if unregister != nil {
		if _, err = unregister(scope); err != nil {
			return err
		}
	}
	if err = m.Controller.ReleaseInProcessIdleOnOwner(scope, id); err != nil {
		return err
	}
	if err = m.Controller.UpdateBackgroundTaskOnOwner(scope, id, func(data protocol.JSONObject) (protocol.JSONObject, error) {
		data, _, err := teamspawn.FinishTask(data, "killed", now, "")
		return data, err
	}); err != nil {
		return err
	}
	if err = m.Controller.SetInProcessWorkCancelOnOwner(scope, id, nil); err != nil {
		return err
	}
	if m.teamState != nil {
		snapshot := m.teamState.snapshot.Clone()
		teammates, ok := snapshot.Object("teammates")
		if ok {
			teammates = teammates.Clone()
			teammates.Delete(agentID)
			snapshot.Set("teammates", rawMain(teammates))
			m.teamState.snapshot = snapshot
			m.publishTerminalTeamFooter(scope)
		}
	}
	write, err := nativeSkillWork(m, scope, func() (struct{}, error) {
		_, err := m.teamLeader.config.RemoveMemberByAgentID(team, agentID)
		return struct{}{}, err
	})
	if err == nil {
		_, err = write.Await(scope)
	}
	// Persistence failure cannot suppress the original closing bookend.
	event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("task_notification")}, protocol.JSONField{Name: "task_id", Value: quoteMain(id)}, protocol.JSONField{Name: "tool_use_id", Value: task.Data.Get("toolUseId")}, protocol.JSONField{Name: "status", Value: quoteMain("stopped")}, protocol.JSONField{Name: "output_file", Value: quoteMain("")}, protocol.JSONField{Name: "summary", Value: task.Data.Get("description")}, protocol.JSONField{Name: "owner_agent_id", Value: identity.Get("agentId")})
	emitErr := m.nativeBackgroundBindings().Emit(scope, event)
	path := task.Data.String("outputFile")
	if path != "" {
		_ = os.Remove(path)
	}
	// The running loop normally evicts eagerly. This Source three-second
	// deadline covers a blocked external tool without retaining its UI history.
	time.AfterFunc(3*time.Second, func() {
		_, _ = inProcessParentCall(context.Background(), m, func(next *cooperative.Scope) (struct{}, error) {
			current, err := m.Controller.BackgroundTaskOnOwner(next, id)
			if err != nil {
				return struct{}{}, err
			}
			if current != nil && current.Data.String("status") != "running" && string(current.Data.Get("notified")) == "true" {
				err = m.Controller.DeleteBackgroundTaskOnOwner(next, id)
			}
			return struct{}{}, err
		})
	})
	return errors.Join(err, emitErr)
}
