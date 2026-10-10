package clibootstrap

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"time"
)

// nativeLeaderInboxOnOwner is one Source headless poll/queue continuation. It
// runs after ordinary query finally, before the next ASK or stdin completion.
// True means recheck the canonical queue; no second Harness is constructed.
func (m *MainPrint) nativeLeaderInboxOnOwner(ctx context.Context, scope *cooperative.Scope, inputClosed bool) (bool, error) {
	if !scope.BelongsTo(m.Owner) {
		return false, cooperative.ErrRestricted
	}
	if m.options.Interactive {
		return false, nil
	}
	if m.teamWorker != nil || m.options.DetachedAgent != nil || m.teamState == nil || m.teamLeader == nil {
		return false, nil
	}
	team := m.teamState.snapshot.String("teamName")
	if team == "" || m.teamState.snapshot.String("leadAgentId") == "" {
		return false, nil
	}
	tasks, err := m.Controller.BackgroundTasksOnOwner(scope)
	if err != nil {
		return false, err
	}
	if !teamspawn.HasTeammatesRequiringShutdown(m.teamState.snapshot, tasks) {
		return false, nil
	}
	if context.Cause(ctx) != nil {
		return false, context.Cause(ctx)
	}
	if m.teamLeader.mailbox == nil {
		m.teamLeader.mailbox, err = teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore, Log: func(err error) { _, _ = fmt.Fprintln(m.options.Error, err) }})
		if err != nil {
			return false, err
		}
	}
	reader := teamspawn.LeaderInbox{Mailbox: m.teamLeader.mailbox, Config: m.teamLeader.config, Tasks: m.taskStore}
	snapshot := m.teamState.snapshot.Clone()
	read, err := nativeSkillWork(m, scope, func() (teamspawn.LeadMailboxRead, error) { return reader.Read(team, snapshot) })
	if err != nil {
		return false, err
	}
	result, err := read.Await(scope)
	if err != nil {
		return false, err
	}
	if m.teamState != nil && m.teamState.snapshot.String("teamName") == team && len(result.Removed) > 0 {
		current := m.teamState.snapshot.Clone()
		roster, ok := current.Object("teammates")
		if ok {
			roster = roster.Clone()
			for _, id := range result.Removed {
				roster.Delete(id)
			}
			current.Set("teammates", rawMain(roster))
			m.teamState.snapshot = current
		}
	}
	part := result.Partition
	if len(part.PermissionRequests) > 0 || len(part.SandboxPermissionRequests) > 0 {
		if err = m.startNativeLeaderPermissions(scope, team, part); err != nil {
			return false, err
		}
	}
	if len(part.Remaining) > 0 {
		return true, m.Controller.EnqueueTeamPromptOnOwner(scope, result.Formatted, false)
	}
	if len(part.PermissionRequests) > 0 || len(part.SandboxPermissionRequests) > 0 {
		return true, nil
	}
	if inputClosed && !m.teamLeader.shutdownPromptInjected {
		m.teamLeader.shutdownPromptInjected = true
		return true, m.Controller.EnqueueTeamPromptOnOwner(scope, quoteMain(teamspawn.ShutdownTeamPrompt), true)
	}
	wake := make(chan struct{})
	timer := time.AfterFunc(500*time.Millisecond, func() { close(wake) })
	defer timer.Stop()
	if err = scope.AwaitPromise(wake); err != nil {
		return false, err
	}
	return true, nil
}

func (m *MainPrint) startNativeLeaderPermissions(scope *cooperative.Scope, team string, part teamspawn.LeadMailboxPartition) error {
	if m.options.Interactive {
		if m.teamLeader.permissionTools == nil {
			m.teamLeader.permissionTools = map[string]bool{}
		}
		requests := []protocol.JSONObject{}
		for _, entry := range part.PermissionRequests {
			parsed, _ := entry.Object("parsed")
			key := string(parsed.Get("tool_use_id"))
			if m.teamLeader.permissionTools[key] {
				continue
			}
			m.teamLeader.permissionTools[key] = true
			requests = append(requests, entry)
		}
		part.PermissionRequests = requests
	}
	if m.teamLeader.permissionContext == nil {
		life, cancel := context.WithCancel(context.Background())
		m.teamLeader.permissionContext = life
		m.teamLeader.permissionCancel = cancel
		if _, err := m.Cleanup.Register(scope, runtimecleanup.NewCallback(func(next *cooperative.Scope) (*runtimecleanup.Future, error) {
			cancel()
			return nativeSkillWork(m, next, func() (struct{}, error) { return struct{}{}, nil })
		})); err != nil {
			cancel()
			return err
		}
	}
	life := m.teamLeader.permissionContext
	bridge := teamspawn.LeaderPermissionBridge{Mailbox: m.teamLeader.mailbox, TeamName: team, CanPromptHost: m.options.ResolvePermission != nil, Timestamp: func() string { return m.options.Native.Now().UTC().Format("2006-01-02T15:04:05.000Z") }, Log: func(err error) { _, _ = fmt.Fprintln(m.options.Error, err) }, Ask: func(ctx context.Context, request protocol.JSONObject) (protocol.JSONObject, error) {
		return inProcessParentCall(ctx, m, func(next *cooperative.Scope) (protocol.JSONObject, error) {
			input, ok := request.Object("input")
			if !ok {
				return protocol.JSONObject{}, fmt.Errorf("permission input must be an object")
			}
			call := queryloop.JSONToolCallContext{ToolUseID: request.Get("toolUseId"), Opaque: &builtinwrite.CallContext{Scope: next}, PermDecision: &queryloop.ToolPermission{Behavior: "ask", Input: &input, Description: request.Get("description"), DisplayName: request.Get("displayName"), Suggestions: request.Get("permissionSuggestions")}}
			selected, err := m.options.ResolvePermission(ctx, next, queryloop.RegisteredTool{Name: request.String("toolName")}, &input, call)
			result := protocol.NewJSONObject(protocol.JSONField{Name: "behavior", Value: quoteMain(selected.Behavior)}, protocol.JSONField{Name: "message", Value: selected.Message}, protocol.JSONField{Name: "updatedPermissions", Value: selected.UpdatedPermissions})
			if selected.UpdatedInput != nil {
				result.Set("updatedInput", rawMain(*selected.UpdatedInput))
			}
			return result, err
		})
	}}
	work, err := nativeSkillWork(m, scope, func() (struct{}, error) { return struct{}{}, bridge.Resolve(life, part) })
	if err != nil {
		return err
	}
	_, err = work.Then(scope, func(_ *cooperative.Scope, _ struct{}, failure error) error {
		if m.options.Interactive {
			for _, entry := range part.PermissionRequests {
				parsed, _ := entry.Object("parsed")
				delete(m.teamLeader.permissionTools, string(parsed.Get("tool_use_id")))
			}
		}
		if failure != nil {
			_, _ = fmt.Fprintln(m.options.Error, failure)
		}
		return nil
	})
	return err
}
