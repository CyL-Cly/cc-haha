package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

// PollInteractiveLeaderInbox uses a named independent owner callback. Provider
// I/O pumps that callback on the same mutable owner while its ASK is parked.
// It never starts a second ASK or waits for the foreground turn mutex.
func (m *MainPrint) PollInteractiveLeaderInbox(ctx context.Context) ([]protocol.JSONObject, error) {
	var inbox []protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if !m.initialized || m.closed || m.shuttingDown {
			return cooperative.ErrClosed
		}
		if !m.options.Interactive {
			return cooperative.ErrRestricted
		}
		var pollErr error
		if m.teamWorker != nil && m.teamWorker.pane != nil {
			pollErr = m.pollInteractivePaneInboxOnOwner(scope)
		} else {
			pollErr = m.pollInteractiveLeaderInboxOnOwner(scope)
		}
		if err := pollErr; err != nil {
			return err
		}
		inbox = m.Controller.AppState().Inbox
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err = receipt.Wait(); err != nil {
		return nil, err
	}
	return inbox, nil
}

func (m *MainPrint) interactiveLeaderInbox(scope *cooperative.Scope) (*teamspawn.InteractiveInbox, string, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, "", cooperative.ErrRestricted
	}
	if m.teamState == nil || m.teamLeader == nil || m.teamWorker != nil || m.options.DetachedAgent != nil || m.nativeInProcessTeammateIdentity() != nil {
		return nil, "", nil
	}
	team := m.teamState.snapshot.String("teamName")
	if team == "" || m.teamState.snapshot.String("leadAgentId") == "" {
		return nil, "", nil
	}
	if m.teamLeader.mailbox == nil {
		var err error
		m.teamLeader.mailbox, err = teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.ConfigHomeDirectory(), TaskStore: m.taskStore, Log: func(err error) { _, _ = fmt.Fprintln(m.options.Error, err) }})
		if err != nil {
			return nil, "", err
		}
	}
	return &teamspawn.InteractiveInbox{Mailbox: m.teamLeader.mailbox, Config: m.teamLeader.config, Tasks: m.taskStore}, team, nil
}

func (m *MainPrint) pollInteractiveLeaderInboxOnOwner(scope *cooperative.Scope) error {
	reader, team, err := m.interactiveLeaderInbox(scope)
	if err != nil || reader == nil {
		return err
	}
	work, err := nativeSkillWork(m, scope, func() (teamspawn.InteractiveInboxRead, error) { return reader.Read(team) })
	if err != nil {
		return err
	}
	read, err := work.Await(scope)
	if err != nil {
		return err
	}
	if read.Unread == 0 {
		return nil
	}
	if m.teamState == nil || m.teamState.snapshot.String("teamName") != team {
		return nil
	}
	// Tool lookup matches the hook's base catalog; an unknown tool is consumed
	// without queuing a UI request. MCP names are not base tool permissions.
	_, tools, err := m.nativeTools.Catalog(scope)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, tool := range tools {
		known[tool.Name] = true
	}
	requests := []protocol.JSONObject{}
	for _, entry := range read.Permissions.PermissionRequests {
		parsed, _ := entry.Object("parsed")
		if known[parsed.String("tool_name")] {
			requests = append(requests, entry)
		}
	}
	read.Permissions.PermissionRequests = requests
	if len(requests) > 0 || len(read.Permissions.SandboxPermissionRequests) > 0 {
		if err := m.startNativeLeaderPermissions(scope, team, read.Permissions); err != nil {
			return err
		}
	}
	mode, err := m.Controller.PlanModeContext(scope)
	if err != nil {
		return err
	}
	inherit := mode.String("mode")
	if inherit == "plan" {
		inherit = "default"
	}
	stamp := m.options.Native.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	for _, entry := range read.Plans {
		parsed, _ := entry.Object("parsed")
		message, _ := entry.Object("message")
		response := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("plan_approval_response")}, protocol.JSONField{Name: "requestId", Value: parsed.Get("requestId")}, protocol.JSONField{Name: "approved", Value: rawMain(true)}, protocol.JSONField{Name: "timestamp", Value: quoteMain(stamp)}, protocol.JSONField{Name: "permissionMode", Value: quoteMain(inherit)})
		envelope := protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: quoteMain("team-lead")}, protocol.JSONField{Name: "text", Value: quoteMain(string(rawMain(response)))}, protocol.JSONField{Name: "timestamp", Value: quoteMain(stamp)})
		write, err := nativeSkillWork(m, scope, func() (struct{}, error) {
			return struct{}{}, reader.Mailbox.Write(message.String("from"), team, envelope)
		})
		if err != nil {
			return err
		}
		if _, err = write.Await(scope); err != nil {
			return err
		}
		// The response resets the task's awaiting flag. Its own loop later
		// applies the inherited permission mode from the actual mailbox.
		tasks, err := m.Controller.BackgroundTasksOnOwner(scope)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			identity, _ := task.Object("identity")
			if task.String("type") == "in_process_teammate" && identity.String("agentName") == message.String("from") {
				if err := m.Controller.UpdateBackgroundTaskOnOwner(scope, task.String("id"), func(data protocol.JSONObject) (protocol.JSONObject, error) {
					data.Set("awaitingPlanApproval", rawMain(false))
					return data, nil
				}); err != nil {
					return err
				}
			}
		}
	}
	for _, member := range read.Removed {
		if member.String("backendType") == "tmux" || member.String("backendType") == "iterm2" {
			if member.String("tmuxPaneId") != "" {
				panes := m.teamLeader.panes
				kill, err := nativeSkillWork(m, scope, func() (bool, error) {
					return panes.KillPane(member.String("backendType"), member.String("tmuxPaneId")), nil
				})
				if err != nil {
					return err
				}

				if _, err = kill.Then(scope, func(_ *cooperative.Scope, _ bool, failure error) error {
					if failure != nil {
						_, _ = fmt.Fprintln(m.options.Error, failure)
					}
					return nil
				}); err != nil {
					return err
				}
			}
		}
		remove, err := nativeSkillWork(m, scope, func() (protocol.JSONObject, error) { return reader.RemoveApproved(team, member) })
		if err != nil {
			return err
		}
		notification, err := remove.Await(scope)
		if err != nil {
			return err
		}
		notification.Set("timestamp", quoteMain(stamp))
		if m.teamState == nil || m.teamState.snapshot.String("teamName") != team {
			return nil
		}
		roster, ok := m.teamState.snapshot.Object("teammates")
		id := member.String("agentId")
		if ok && roster.Get(id) != nil {
			roster = roster.Clone()
			roster.Delete(id)
			snapshot := m.teamState.snapshot.Clone()
			snapshot.Set("teammates", rawMain(roster))
			m.teamState.snapshot = snapshot
			m.publishTerminalTeamFooter(scope)
			tasks, err := m.Controller.BackgroundTasksOnOwner(scope)
			if err != nil {
				return err
			}
			for _, task := range tasks {
				identity, _ := task.Object("identity")
				if task.String("type") == "in_process_teammate" && identity.String("agentId") == id {
					if err := m.Controller.UpdateBackgroundTaskOnOwner(scope, task.String("id"), func(data protocol.JSONObject) (protocol.JSONObject, error) {
						data.Set("status", quoteMain("completed"))
						data.Set("endTime", rawMain(m.options.Native.NowMillis()))
						return data, nil
					}); err != nil {
						return err
					}
				}
			}
			if err := m.Controller.QueueTeamInboxOnOwner(scope, []protocol.JSONObject{notification}); err != nil {
				return err
			}
		}
	}
	if err := m.Controller.QueueTeamInboxOnOwner(scope, read.Regular); err != nil {
		return err
	}
	mark, err := nativeSkillWork(m, scope, func() (struct{}, error) {
		return struct{}{}, reader.MarkDelivered(team, read.DeferShutdown, read.Candidates)
	})
	if err != nil {
		return err
	}
	_, err = mark.Await(scope)
	return err
}

func (m *MainPrint) nativeTeamMailboxAttachments(scope *cooperative.Scope, callAgentID string) ([]queryloop.Message, error) {
	if m.options.Native.Environment["USER_TYPE"] != "ant" {
		return nil, nil
	}
	enabled, gateErr := m.nativeSendMessageEnabled(scope)
	if gateErr != nil || !enabled {
		return nil, gateErr
	}
	appState := m.Controller.AppState()
	reader, team, err := m.interactiveLeaderInbox(scope)
	roleName := ""
	inProcess := m.nativeInProcessTeammateIdentity()
	sharedViewedName := ""
	if inProcess != nil {
		actual, e := m.Controller.TeammateIdentityOnOwner(scope)
		if e != nil || actual != *inProcess || callAgentID != m.options.AgentID {
			return nil, cooperative.ErrRestricted
		}
		if m.teamLeader == nil {
			return nil, cooperative.ErrRestricted
		}
		if m.teamLeader.mailbox == nil {
			m.teamLeader.mailbox, e = teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.ConfigHomeDirectory(), TaskStore: m.taskStore})
			if e != nil {
				return nil, e
			}
		}
		if d := m.options.DetachedAgent; d.preset != nil && d.preset.parent != nil {
			viewed, e := nativeAgentOwnerCall(m, scope, d.preset.parent, func(parentScope *cooperative.Scope) (string, error) {
				state := d.preset.parent.Controller.AppState()
				if task := state.Tasks[state.ViewingAgentTaskID]; task != nil && task.Data.String("type") == "in_process_teammate" {
					identity, _ := task.Data.Object("identity")
					return identity.String("agentName"), nil
				}
				return "", nil
			})
			if e != nil {
				return nil, e
			}
			sharedViewedName = viewed
		}
		reader = &teamspawn.InteractiveInbox{Mailbox: m.teamLeader.mailbox, Config: m.teamLeader.config, Tasks: m.taskStore}
		team = actual.TeamName
		roleName = actual.AgentName
	} else if m.teamWorker != nil {
		mailbox, config, e := m.nativeStandaloneTeamMailbox(scope)
		if e != nil {
			return nil, e
		}
		reader = &teamspawn.InteractiveInbox{Mailbox: mailbox, Config: config, Tasks: m.taskStore}
		team = m.teamWorker.preset.Start.TeamName
		roleName = m.teamWorker.preset.Start.Name
	} else if callAgentID != "" {
		return nil, nil
	}
	if err != nil || reader == nil {
		return nil, err
	}
	pending, err := m.Controller.LeaderPendingTeamInboxOnOwner(scope)
	if err != nil {
		return nil, err
	}
	agentName := "team-lead"
	if m.teamState != nil {
		if teammates, ok := m.teamState.snapshot.Object("teammates"); ok {
			if lead, ok := teammates.Object(m.teamState.snapshot.String("leadAgentId")); ok && lead.String("name") != "" {
				agentName = lead.String("name")
			}
		}
	}
	if roleName != "" {
		agentName = roleName
	}
	if inProcess != nil {
		pending = nil
	}
	if task := appState.Tasks[appState.ViewingAgentTaskID]; task != nil && task.Data.String("type") == "in_process_teammate" {
		identity, _ := task.Data.Object("identity")
		agentName = identity.String("agentName")
		pending = nil
	}
	if sharedViewedName != "" {
		agentName = sharedViewedName
		pending = nil
	}
	if agentName == "" {
		return nil, nil
	}
	file, err := nativeSkillWork(m, scope, func() ([]protocol.JSONObject, error) {
		raw, err := reader.Mailbox.Unread(agentName, team)
		if err != nil {
			return nil, err
		}
		rows := []protocol.JSONObject{}
		for _, value := range raw {
			var row protocol.JSONObject
			_ = row.UnmarshalJSON(value)
			if !teamspawn.StructuredProtocolMessage(row.String("text")) {
				rows = append(rows, row)
			}
		}
		candidates := []json.RawMessage{}
		for _, row := range rows {
			candidates = append(candidates, rawMain(row))
		}
		claimed, e := reader.Mailbox.Claim(agentName, team, candidates)
		if e != nil {
			return nil, e
		}
		owned := []protocol.JSONObject{}
		for _, v := range claimed {
			var row protocol.JSONObject
			_ = row.UnmarshalJSON(v)
			owned = append(owned, row)
		}
		return owned, nil
	})
	if err != nil {
		return nil, err
	}
	unread, err := file.Await(scope)
	if err != nil {
		return nil, err
	}
	// Source captures its AppState before awaiting the physical mailbox read.
	attachment := teamspawn.TeammateMailboxAttachment(unread, pending)
	if !attachment.IsObject() {
		return nil, nil
	}
	message := queryloop.Message{Record: m.factory.CreateAttachment(rawMain(attachment))}
	// Match Source's final acknowledgement of the AppState batch only after
	// the attachment exists. File rows were already atomically claimed above.
	if len(pending) > 0 {
		if err := m.Controller.ProcessTeamInboxOnOwner(scope, pending); err != nil {
			return nil, err
		}
	}
	return []queryloop.Message{message}, nil
}

func (m *MainPrint) enqueueInteractiveLeaderInbox(scope *cooperative.Scope) (bool, error) {
	pending, err := m.Controller.LeaderPendingTeamInboxOnOwner(scope)
	if err != nil {
		return false, err
	}
	if err := m.Controller.FinishTeamInboxOnOwner(scope, nil); err != nil {
		return false, err
	}
	if len(pending) == 0 {
		return false, nil
	}
	queued, err := m.Controller.HasLeaderQueuedUserInputOnOwner(scope)
	if err != nil || queued {
		return false, err
	}
	formatted, err := teamspawn.FormatInteractiveTeammateMessages(pending)
	if err != nil {
		return false, err
	}
	if err = m.Controller.EnqueueTeamPromptOnOwner(scope, formatted, false); err != nil {
		return false, err
	}
	return true, m.Controller.FinishTeamInboxOnOwner(scope, pending)
}
