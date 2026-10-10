package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func inProcessParentCall[T any](ctx context.Context, m *MainPrint, run func(*cooperative.Scope) (T, error)) (T, error) {
	var value T
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error { var err error; value, err = run(scope); return err })
	if err == nil {
		err = receipt.Wait()
	}
	return value, err
}
func (m *MainPrint) updateInProcessTask(ctx context.Context, id string, update func(protocol.JSONObject) (protocol.JSONObject, error)) error {
	_, err := inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (struct{}, error) {
		return struct{}{}, m.Controller.UpdateBackgroundTaskOnOwner(scope, id, update)
	})
	return err
}

// runNativeInProcessLoop consumes the actual registered AppState task. The
// spawning producer owns cancellation/cleanup; every iteration uses the shared
// independent MainPrint Agent graph, then waits on the same physical inbox.
func (m *MainPrint) runNativeInProcessLoop(ctx context.Context, taskID string, identity sessioncontroller.InProcessTeammateIdentity, definition *agentcatalog.Definition, model, prompt, description string) error {
	task, err := inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (*sessioncontroller.BackgroundTask, error) {
		return m.Controller.BackgroundTaskOnOwner(scope, taskID)
	})
	if err != nil {
		return err
	}
	if task == nil || task.Data.String("type") != "in_process_teammate" || task.Data.String("status") != "running" {
		return cooperative.ErrRestricted
	}
	stored, _ := task.Data.Object("identity")
	if stored.String("agentId") != identity.AgentID || stored.String("agentName") != identity.AgentName || stored.String("teamName") != identity.TeamName || identity.ParentSessionID != m.Controller.SessionID() {
		return cooperative.ErrRestricted
	}
	config, err := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore, PID: m.options.PID, Platform: m.options.Platform})
	if err != nil {
		return err
	}
	mailbox, err := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore, Log: func(err error) { _, _ = fmt.Fprintln(m.options.Error, err) }})
	if err != nil {
		return err
	}
	type pending struct {
		value string
		found bool
	}
	inbox, err := teamspawn.NewInbox(teamspawn.InboxOptions{Mailbox: mailbox, Tasks: m.taskStore, TeamName: identity.TeamName, AgentName: identity.AgentName, Log: func(err error) { _, _ = fmt.Fprintln(m.options.Error, err) }, ReadPending: func(ctx context.Context) (string, bool, error) {
		value, err := inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (pending, error) {
			var out pending
			err := m.Controller.UpdateBackgroundTaskOnOwner(scope, taskID, func(task protocol.JSONObject) (protocol.JSONObject, error) {
				var rows []string
				if err := json.Unmarshal(task.Get("pendingUserMessages"), &rows); err != nil {
					return task, err
				}
				if len(rows) > 0 {
					out = pending{rows[0], true}
					task.Set("pendingUserMessages", rawMain(rows[1:]))
				}
				return task, nil
			})
			return out, err
		})
		return value.value, value.found, err
	}})
	if err != nil {
		return err
	}
	history := []protocol.JSONObject{}
	current := teamspawn.TeammateMessage("team-lead", prompt, "", description)
	streamScopeID := nativeTeamStreamScope(config.Read(identity.TeamName))
	if err = m.appendNativeInProcessPrompt(ctx, taskID, current); err != nil {
		return err
	}
	for ctx.Err() == nil {
		mode, err := inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (string, error) {
			task, err := m.Controller.BackgroundTaskOnOwner(scope, taskID)
			if err != nil {
				return "", err
			}
			if task == nil || task.Data.String("status") != "running" {
				return "", cooperative.ErrClosed
			}
			return task.Data.String("permissionMode"), nil
		})
		if err != nil {
			return err
		}
		if err = m.updateInProcessTask(ctx, taskID, func(task protocol.JSONObject) (protocol.JSONObject, error) {
			task.Set("isIdle", rawMain(false))
			return task, nil
		}); err != nil {
			return err
		}
		_, failure := config.Mutate(identity.TeamName, func(team protocol.JSONObject) (protocol.JSONObject, error) {
			var members []protocol.JSONObject
			_ = json.Unmarshal(team.Get("members"), &members)
			for i := range members {
				if members[i].String("name") == identity.AgentName {
					members[i].Set("isActive", rawMain(true))
					break
				}
			}
			team.Set("members", rawMain(members))
			return team, nil
		})
		if failure != nil {
			_, _ = fmt.Fprintln(m.options.Error, failure)
		}
		progress, err := m.newNativeInProcessProgress(ctx)
		if err != nil {
			return err
		}
		work, cancelWork := context.WithCancelCause(ctx)
		if _, err = inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (struct{}, error) {
			return struct{}{}, m.Controller.SetInProcessWorkCancelOnOwner(scope, taskID, cancelWork)
		}); err != nil {
			cancelWork(err)
			return err
		}
		accepted := map[string]bool{}
		run, failure := m.runNativeInProcessTurn(work, nativeInProcessTurn{Identity: identity, Definition: definition, Model: model, PermissionMode: mode, Prompt: current, Description: description, History: history, StreamScopeID: streamScopeID, Observe: func(scope *cooperative.Scope, row protocol.JSONObject) error {
			if work.Err() != nil {
				return nil
			}
			accepted[row.String("uuid")] = true
			return m.observeNativeInProcessMessage(scope, taskID, progress, row)
		}})
		workWasAborted := work.Err() != nil && ctx.Err() == nil
		cancelWork(nil)
		if _, err = inProcessParentCall(context.Background(), m, func(scope *cooperative.Scope) (struct{}, error) {
			return struct{}{}, m.Controller.SetInProcessWorkCancelOnOwner(scope, taskID, nil)
		}); err != nil {
			return err
		}
		_, activityFailure := config.Mutate(identity.TeamName, func(team protocol.JSONObject) (protocol.JSONObject, error) {
			var members []protocol.JSONObject
			_ = json.Unmarshal(team.Get("members"), &members)
			for i := range members {
				if members[i].String("name") == identity.AgentName {
					members[i].Set("isActive", rawMain(false))
					break
				}
			}
			team.Set("members", rawMain(members))
			return team, nil
		})
		if activityFailure != nil {
			_, _ = fmt.Fprintln(m.options.Error, activityFailure)
		}
		if workWasAborted {
			for index, row := range run.NewMessages {
				if index == 0 || accepted[row.String("uuid")] {
					history = append(history, row)
				}
			}
		} else {
			history = append(history, run.NewMessages...)
		}
		if ctx.Err() != nil {
			return nil
		}
		if failure != nil && !workWasAborted {
			return failure
		}
		if workWasAborted {
			if err = m.appendInProcessInterrupt(ctx, taskID); err != nil {
				return err
			}
		}
		wasAlreadyIdle, err := inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (bool, error) {
			return m.Controller.MarkInProcessIdleOnOwner(scope, taskID)
		})
		if err != nil {
			return err
		}
		stamp := m.options.Native.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		idleReason := "available"
		if workWasAborted {
			idleReason = "interrupted"
		}
		notification := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("idle_notification")}, protocol.JSONField{Name: "from", Value: quoteMain(identity.AgentName)}, protocol.JSONField{Name: "timestamp", Value: quoteMain(stamp)}, protocol.JSONField{Name: "idleReason", Value: quoteMain(idleReason)})
		summary, err := teamspawn.LastPeerDMSummary(history)
		if err != nil {
			return err
		}
		notification.Set("summary", summary)
		envelope := protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: quoteMain(identity.AgentName)}, protocol.JSONField{Name: "text", Value: quoteMain(string(rawMain(notification)))}, protocol.JSONField{Name: "timestamp", Value: quoteMain(stamp)})
		if identity.Color != "" {
			envelope.Set("color", quoteMain(identity.Color))
		}
		if !wasAlreadyIdle {
			if err = mailbox.Write("team-lead", identity.TeamName, envelope); err != nil {
				return err
			}
		}
		next, err := inbox.Wait(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		switch next.String("type") {
		case "aborted":
			return nil
		case "new_message":
			if next.String("from") == "user" {
				current = next.String("message")
			} else {
				current = teamspawn.TeammateMessage(next.String("from"), next.String("message"), next.String("color"), next.String("summary"))
				if err = m.appendNativeInProcessPrompt(ctx, taskID, current); err != nil {
					return err
				}
			}
		case "shutdown_request":
			request, _ := next.Object("request")
			current = teamspawn.TeammateMessage(request.String("from"), next.String("originalMessage"), "", "")
			if err = m.appendNativeInProcessPrompt(ctx, taskID, current); err != nil {
				return err
			}
		default:
			return errors.New("Invalid original teammate wait result")
		}
	}
	return nil
}
