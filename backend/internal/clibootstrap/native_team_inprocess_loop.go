package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
	"time"
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
func (m *MainPrint) runNativeInProcessLoop(ctx context.Context, taskID string, identity sessioncontroller.InProcessTeammateIdentity, definition *agentcatalog.Definition, model, prompt, description string, resumes ...*nativeInProcessResume) (returned error) {
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
	lastDeliveredResult := ""
	defer func() {
		if returned != nil && ctx.Err() == nil {
			result, has, _ := teamspawn.TeammateTurnResult(history)
			if result == lastDeliveredResult {
				has = false
			}
			if reportErr := m.sendNativeInProcessTurnReport(context.Background(), identity, history, "failed", returned.Error(), result, has); reportErr != nil {
				_, _ = fmt.Fprintln(m.options.Error, reportErr)
			}
		}
	}()
	from := "team-lead"
	if len(resumes) > 0 && resumes[0] != nil {
		history = append(history, resumes[0].History...)
		from = resumes[0].From
	}
	if identity.ResumableAgentID == "" {
		identity.ResumableAgentID, err = nativeAgentID()
		if err != nil {
			return err
		}
	}
	delays := m.options.NativeInProcessAutoContinueDelays
	if delays == nil {
		delays = teamspawn.AutoContinueDelays
	}
	attempts := 0
	current := teamspawn.TeammateMessage(from, prompt, "", description)
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
		turnFailure, err := teamspawn.ClassifyTeammateTurnFailure(run.NewMessages)
		if err != nil {
			return err
		}
		if failure != nil && !workWasAborted && turnFailure == nil {
			return failure
		}
		retryCancelled := false
		if !workWasAborted && turnFailure != nil && turnFailure.Transient && attempts < len(delays) {
			attempts++
			retry, cancelRetry := context.WithCancelCause(ctx)
			if _, err = inProcessParentCall(ctx, m, func(scope *cooperative.Scope) (struct{}, error) {
				return struct{}{}, m.Controller.SetInProcessWorkCancelOnOwner(scope, taskID, cancelRetry)
			}); err != nil {
				cancelRetry(err)
				return err
			}
			claim := false
			next, waitErr := inbox.Wait(ctx, teamspawn.WaitOptions{Deadline: time.Now().Add(delays[attempts-1]), CancelContext: retry, ClaimTasks: &claim})
			cancelRetry(nil)
			if _, err = inProcessParentCall(context.Background(), m, func(scope *cooperative.Scope) (struct{}, error) {
				return struct{}{}, m.Controller.SetInProcessWorkCancelOnOwner(scope, taskID, nil)
			}); err != nil {
				return err
			}
			if waitErr != nil {
				return waitErr
			}
			if next.String("type") == "timeout" {
				current = teamworker.FormatTeammateAutoContinuePrompt(turnFailure.Reason, attempts, len(delays))
				if err = m.appendNativeInProcessPrompt(ctx, taskID, current); err != nil {
					return err
				}
				continue
			}
			if next.String("type") != "cancelled" {
				current, err = m.takeNativeInProcessNextPrompt(ctx, taskID, next)
				if err != nil {
					return err
				}
				if next.String("type") == "aborted" {
					return nil
				}
				attempts = 0
				continue
			}
			retryCancelled = true
		}
		if turnFailure == nil {
			attempts = 0
		}
		result, hasResult, err := teamspawn.TeammateTurnResult(history)
		if err != nil {
			return err
		}
		if workWasAborted {
			hasResult = false
		}
		failureReason := ""
		var failureReasonRaw json.RawMessage
		if turnFailure != nil {
			failureReason = turnFailure.Reason
			failureReasonRaw = turnFailure.ReasonRaw
			if retryCancelled {
				failureReason = nativeTeammateFailureNote(failureReason, "(automatic retry cancelled)")
				failureReasonRaw = nativeTeammateFailureNoteRawJSON(failureReasonRaw, "(automatic retry cancelled)")
			} else if turnFailure.Transient {
				failureReason = nativeTeammateFailureNote(failureReason, "(automatic retries exhausted; message "+identity.AgentName+" to continue)")
				failureReasonRaw = nativeTeammateFailureNoteRawJSON(failureReasonRaw, "(automatic retries exhausted; message "+identity.AgentName+" to continue)")
			}
		}
		if !workWasAborted {
			delivery, mailErr := inbox.PollMailbox()
			if mailErr != nil {
				_, _ = fmt.Fprintln(m.options.Error, mailErr)
			}
			if delivery != nil {
				if hasResult || turnFailure != nil {
					if err = m.sendNativeInProcessTurnReport(ctx, identity, history, "", failureReason, result, hasResult, failureReasonRaw); err != nil {
						return err
					}
					if hasResult {
						lastDeliveredResult = result
					}
				}
				current, err = m.takeNativeInProcessNextPrompt(ctx, taskID, *delivery)
				if err != nil {
					return err
				}
				attempts = 0
				continue
			}
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
		idleReason := "available"
		if workWasAborted {
			idleReason = "interrupted"
		} else if turnFailure != nil {
			idleReason = "failed"
		}
		if !wasAlreadyIdle {
			if err = m.sendNativeInProcessTurnReport(ctx, identity, history, idleReason, failureReason, result, hasResult, failureReasonRaw); err != nil {
				return err
			}
			if hasResult {
				lastDeliveredResult = result
			}
		}
		next, err := inbox.Wait(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		current, err = m.takeNativeInProcessNextPrompt(ctx, taskID, next)
		if err != nil {
			return err
		}
		if next.String("type") == "aborted" {
			return nil
		}
		attempts = 0
	}
	return nil
}
