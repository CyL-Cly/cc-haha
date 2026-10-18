package clibootstrap

import (
	"context"
	"fmt"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

// NativeTerminalTeams is minted only by the actual native Footer consumer.
// It retains that component's team and real file/pane producers; a JSON request
// cannot manufacture an independent config root or another Owner's runtime.
type NativeTerminalTeams struct {
	main             *MainPrint
	team             string
	config           *teamstore.Config
	mailbox          *teamstore.Mailbox
	tasks            *taskstore.Service
	panes            *teamspawn.PaneRuntime
	supportsHideShow bool
	mu               sync.Mutex
	members          map[string]protocol.JSONObject
	ordered          []protocol.JSONObject
}

type NativeTerminalTeamsAction struct {
	dialog           *NativeTerminalTeams
	action, memberID string
	member           protocol.JSONObject
	members          []protocol.JSONObject
}

func (d *NativeTerminalTeams) CaptureAction(action, memberID string) *NativeTerminalTeamsAction {
	d.mu.Lock()
	defer d.mu.Unlock()
	return &NativeTerminalTeamsAction{dialog: d, action: action, memberID: memberID, member: d.members[memberID].Clone(), members: cloneTerminalCachedTeams(d.ordered)}
}

func (m *MainPrint) OpenNativeTerminalTeams(ctx context.Context, initial []protocol.JSONObject) (*NativeTerminalTeams, error) {
	return m.openNativeTerminalTeams(ctx, initial, nil)
}

func (m *MainPrint) openNativeTerminalTeams(ctx context.Context, initial []protocol.JSONObject, captured *NativeTerminalTeamsInitial) (*NativeTerminalTeams, error) {
	var result *NativeTerminalTeams
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if !m.nativeTerminalTeamsRoot(scope) {
			return cooperative.ErrRestricted
		}
		if len(initial) == 0 {
			return cooperative.ErrRestricted
		}
		team := initial[0].String("name")
		if captured == nil {
			actual, err := m.terminalCachedTeams(scope)
			if err != nil {
				return err
			}
			if len(actual) == 0 || team != actual[0].String("name") {
				return cooperative.ErrRestricted
			}
		} else if captured.main != m || len(captured.teams) == 0 || team != captured.teams[0].String("name") {
			return cooperative.ErrRestricted
		}
		config, err := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: m.options.Native.nativeEnvironmentSnapshot()["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore, PID: m.options.PID, Platform: m.options.Platform})
		if err != nil {
			return err
		}
		mailbox, err := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.nativeEnvironmentSnapshot()["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore})
		if err != nil {
			return err
		}
		var panes *teamspawn.PaneRuntime
		supports := false
		if m.teamLeader != nil {
			panes = m.teamLeader.panes
			supports = panes.DialogSupportsHideShow()
		} else {
			environment, err := m.nativeProcessEnvironment(scope)
			if err != nil {
				return err
			}
			panes, err = teamspawn.NewPaneRuntime(m.state.Snapshot().CWD, m.options.PID, environment)
			if err != nil {
				return err
			}
		}
		result = &NativeTerminalTeams{main: m, team: team, config: config, mailbox: mailbox, tasks: m.taskStore, panes: panes, supportsHideShow: supports, members: map[string]protocol.JSONObject{}}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}

func (d *NativeTerminalTeams) live(ctx context.Context) error {
	if d == nil || d.main == nil || d.config == nil || d.mailbox == nil || d.tasks == nil || d.panes == nil {
		return cooperative.ErrRestricted
	}
	m := d.main
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if !scope.BelongsTo(m.Owner) || m.closed || m.shuttingDown || !m.initialized {
			return cooperative.ErrClosed
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}

func (d *NativeTerminalTeams) Snapshot(ctx context.Context, memberID string) (protocol.JSONObject, error) {
	if err := d.live(ctx); err != nil {
		return protocol.JSONObject{}, err
	}
	members, err := d.config.TeammateStatuses(d.team)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if err := ctx.Err(); err != nil {
		return protocol.JSONObject{}, err
	}
	d.mu.Lock()
	d.members = map[string]protocol.JSONObject{}
	supportsHideShow := d.panes.DialogSupportsHideShow()
	d.supportsHideShow = supportsHideShow
	d.ordered = cloneTerminalCachedTeams(members)
	for _, member := range members {
		d.members[member.String("agentId")] = member.Clone()
	}
	member := d.members[memberID].Clone()
	d.mu.Unlock()
	tasks := []protocol.JSONObject{}
	if memberID != "" && member.IsObject() {
		all, err := d.tasks.List(d.team)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		for _, task := range all {
			if owner := task.String("owner"); owner == member.String("agentId") || owner == member.String("name") {
				tasks = append(tasks, task.Clone())
			}
		}
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "teamName", Value: quoteMain(d.team)}, protocol.JSONField{Name: "members", Value: rawMain(members)}, protocol.JSONField{Name: "tasks", Value: rawMain(tasks)}, protocol.JSONField{Name: "supportsHideShow", Value: rawMain(supportsHideShow)}), ctx.Err()
}

func (d *NativeTerminalTeams) Action(ctx context.Context, action, memberID string) error {
	return d.CaptureAction(action, memberID).Run(ctx)
}

func (a *NativeTerminalTeamsAction) Run(ctx context.Context) (returned error) {
	if a == nil || a.dialog == nil {
		return cooperative.ErrRestricted
	}
	d := a.dialog
	defer func() {
		if returned == nil || ctx.Err() != nil {
			return
		}
		prefix := ""
		switch a.action {
		case "teams-kill":
			prefix = "Failed to remove teammate"
		case "teams-prune-idle":
			prefix = "Failed to remove idle teammates"
		case "teams-cycle-mode":
			if a.memberID == "" {
				prefix = "Failed to change teammate modes"
			} else {
				prefix = "Failed to change teammate mode"
			}
		}
		if prefix != "" {
			d.debug(ctx, "[TeamsDialog] "+prefix+": "+returned.Error())
		}
	}()
	if err := d.live(ctx); err != nil {
		return err
	}
	action, memberID := a.action, a.memberID
	member := a.member.Clone()
	members := cloneTerminalCachedTeams(a.members)
	if action == "teams-prune-idle" || action == "teams-cycle-mode" && memberID == "" || action == "teams-hide-all-toggle" {
	} else if !member.IsObject() {
		return nil
	} else {
		members = []protocol.JSONObject{member}
	}
	switch action {
	case "teams-view":
		// Source immediately closes the component after initiating this work.
		return d.panes.DialogPane(ctx, "view", member.String("backendType"), member.String("tmuxPaneId"))
	case "teams-hide-toggle", "teams-hide-all-toggle":
		// The frozen external Source bodies are empty, even when the cached
		// backend advertises support. No ant-only pane mutation is invented.
		return nil
	case "teams-shutdown":
		var envelope protocol.JSONObject
		err := d.sample(ctx, func(scope *cooperative.Scope) error {
			m := d.main
			stamp := m.options.Native.Now().UTC().Format("2006-01-02T15:04:05.000Z")
			result, err := teamspawn.StructuredMessage(member.String("name"), protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("shutdown_request")}, protocol.JSONField{Name: "reason", Value: quoteMain("Graceful shutdown requested by team lead")}), teamspawn.ProtocolIdentity{AgentName: "team-lead", Timestamp: stamp, Now: m.options.Native.NowMillis()})
			envelope = result.Envelope
			return err
		})
		if err != nil {
			return err
		}
		return d.mailbox.Write(member.String("name"), d.team, envelope)
	case "teams-cycle-mode":
		if len(members) == 0 {
			return nil
		}
		target := terminalTeamsMode(members[0].String("mode"))
		allSame := true
		for _, row := range members {
			if terminalTeamsMode(row.String("mode")) != target {
				allSame = false
			}
		}
		if !allSame {
			target = "default"
		} else {
			err := d.sample(ctx, func(scope *cooperative.Scope) error {
				permission, err := d.main.Host.NativePermissionContext(scope)
				if err != nil {
					return err
				}
				context := protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quoteMain(target)}, protocol.JSONField{Name: "isBypassPermissionsModeAvailable", Value: permission.Get("isBypassPermissionsModeAvailable")})
				target = sdkpermissions.NextTerminalMode(context, d.main.options.Native.nativeEnvironmentSnapshot()["USER_TYPE"])
				return nil
			})
			if err != nil {
				return err
			}
		}
		updates := make([]teamstore.MemberModeUpdate, len(members))
		for i, row := range members {
			updates[i] = teamstore.MemberModeUpdate{MemberName: row.String("name"), Mode: target}
		}
		var err error
		if memberID == "" {
			_, err = d.config.SetMultipleMemberModes(d.team, updates)
		} else {
			_, err = d.config.SetMemberMode(d.team, member.String("name"), target)
		}
		if err != nil {
			return err
		}
		for _, row := range members {
			if err := ctx.Err(); err != nil {
				return err
			}
			var stamp string
			if err = d.sample(ctx, func(*cooperative.Scope) error {
				stamp = d.main.options.Native.Now().UTC().Format("2006-01-02T15:04:05.000Z")
				return nil
			}); err != nil {
				return err
			}
			message := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("mode_set_request")}, protocol.JSONField{Name: "mode", Value: quoteMain(target)}, protocol.JSONField{Name: "from", Value: quoteMain("team-lead")})
			envelope := protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: quoteMain("team-lead")}, protocol.JSONField{Name: "text", Value: quoteMain(string(rawMain(message)))}, protocol.JSONField{Name: "timestamp", Value: quoteMain(stamp)})
			if err = d.mailbox.Write(row.String("name"), d.team, envelope); err != nil {
				return err
			}
		}
		return nil
	case "teams-prune-idle":
		var jobs sync.WaitGroup
		failures := make(chan error, len(members))
		for _, row := range members {
			if row.String("status") != "idle" {
				continue
			}
			jobs.Add(1)
			go func(row protocol.JSONObject) {
				defer jobs.Done()
				if err := d.kill(ctx, row); err != nil {
					failures <- err
				}
			}(row.Clone())
		}
		jobs.Wait()
		close(failures)
		for err := range failures {
			return err
		}
		return nil
	case "teams-kill":
		for _, row := range members {
			if err := d.kill(ctx, row); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("unknown Teams dialog action")
}

func (d *NativeTerminalTeams) Tasks(ctx context.Context, memberID string) ([]protocol.JSONObject, error) {
	d.mu.Lock()
	member := d.members[memberID].Clone()
	d.mu.Unlock()
	return d.tasksForMember(ctx, member)
}

func (d *NativeTerminalTeams) tasksForMember(ctx context.Context, member protocol.JSONObject) ([]protocol.JSONObject, error) {
	if err := d.live(ctx); err != nil {
		return nil, err
	}
	result := []protocol.JSONObject{}
	if !member.IsObject() {
		return result, nil
	}
	all, err := d.tasks.List(d.team)
	if err != nil {
		return nil, err
	}
	for _, task := range all {
		if owner := task.String("owner"); owner == member.String("agentId") || owner == member.String("name") {
			result = append(result, task.Clone())
		}
	}
	return result, ctx.Err()
}

func terminalTeamsMode(mode string) string {
	switch mode {
	case "default", "acceptEdits", "plan", "bypassPermissions", "dontAsk", "auto":
		return mode
	}
	return "default"
}
func (d *NativeTerminalTeams) sample(ctx context.Context, run func(*cooperative.Scope) error) error {
	receipt, err := d.main.Owner.SubmitIndependent(ctx, cooperative.Read, run)
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
func (d *NativeTerminalTeams) kill(ctx context.Context, member protocol.JSONObject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if backend := member.String("backendType"); backend != "" {
		if err := d.panes.DialogPane(ctx, "kill", backend, member.String("tmuxPaneId")); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			d.debug(ctx, "[TeamsDialog] Failed to kill pane "+member.String("tmuxPaneId")+": "+err.Error())
		}
	} else {
		d.debug(ctx, "[TeamsDialog] Skipping pane kill for "+member.String("tmuxPaneId")+": no backendType recorded")
	}
	if _, err := d.config.RemoveMemberByPaneID(d.team, member.Get("tmuxPaneId")); err != nil {
		return err
	}
	result, err := d.tasks.UnassignTeammateTasks(d.team, member.String("agentId"), member.String("name"), "terminated")
	if err != nil {
		return err
	}
	return d.sample(ctx, func(scope *cooperative.Scope) error {
		m := d.main
		if m.closed || m.teamState == nil {
			return nil
		}
		roster, ok := m.teamState.snapshot.Object("teammates")
		id := member.String("agentId")
		if !ok || roster.Get(id) == nil {
			return nil
		}
		roster = roster.Clone()
		roster.Delete(id)
		snapshot := m.teamState.snapshot.Clone()
		snapshot.Set("teammates", rawMain(roster))
		m.teamState.snapshot = snapshot
		m.publishTerminalTeamFooter(scope)
		body := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("teammate_terminated")}, protocol.JSONField{Name: "message", Value: quoteMain(result.NotificationMessage)})
		return m.Controller.QueueTeamInboxOnOwner(scope, []protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: quoteMain("system")}, protocol.JSONField{Name: "text", Value: quoteMain(string(rawMain(body)))}, protocol.JSONField{Name: "timestamp", Value: quoteMain(m.options.Native.Now().UTC().Format("2006-01-02T15:04:05.000Z"))})})
	})
}

func (d *NativeTerminalTeams) debug(ctx context.Context, message string) {
	_ = d.sample(ctx, func(*cooperative.Scope) error {
		if err := d.main.initializeNativeErrorLog(); err != nil {
			return err
		}
		if d.main.errorLogSink != nil {
			return d.main.errorLogSink.Debug(message, "DEBUG")
		}
		return nil
	})
}
