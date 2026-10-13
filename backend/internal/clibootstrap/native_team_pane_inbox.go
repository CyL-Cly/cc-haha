package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"strings"
)

func (m *MainPrint) nativePaneMailbox(scope *cooperative.Scope) (*teamstore.Mailbox, *teamstore.Config, error) {
	if !scope.BelongsTo(m.Owner) || m.teamWorker == nil || m.teamWorker.pane == nil {
		return nil, nil, cooperative.ErrRestricted
	}
	return m.nativeStandaloneTeamMailbox(scope)
}
func (m *MainPrint) nativeStandaloneTeamMailbox(scope *cooperative.Scope) (*teamstore.Mailbox, *teamstore.Config, error) {
	if !scope.BelongsTo(m.Owner) || m.teamWorker == nil {
		return nil, nil, cooperative.ErrRestricted
	}
	identity, err := m.Controller.TeammateIdentityOnOwner(scope)
	if err != nil || identity.AgentID != m.teamWorker.preset.AgentID || identity.TeamName != m.teamWorker.preset.Start.TeamName || identity.AgentName != m.teamWorker.preset.Start.Name {
		return nil, nil, cooperative.ErrRestricted
	}
	w := m.teamWorker
	if w.mailbox == nil {
		var err error
		w.mailbox, err = teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore, Log: func(err error) { _, _ = fmt.Fprintln(m.options.Error, err) }})
		if err != nil {
			return nil, nil, err
		}
		w.config, err = teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore, PID: m.options.PID, Platform: m.options.Platform})
		if err != nil {
			return nil, nil, err
		}
	}
	return w.mailbox, w.config, nil
}
func (m *MainPrint) pollInteractivePaneInboxOnOwner(scope *cooperative.Scope) error {
	mailbox, config, err := m.nativePaneMailbox(scope)
	if err != nil {
		return err
	}
	id := *m.teamWorker.pane
	work, err := nativeSkillWork(m, scope, func() ([]protocol.JSONObject, error) {
		raw, err := mailbox.Unread(id.AgentName, id.TeamName)
		if err != nil {
			return nil, err
		}
		raw, err = mailbox.Claim(id.AgentName, id.TeamName, raw)
		rows := []protocol.JSONObject{}
		for _, value := range raw {
			var row protocol.JSONObject
			_ = row.UnmarshalJSON(value)
			rows = append(rows, row)
		}
		return rows, err
	})
	if err != nil {
		return err
	}
	rows, err := work.Await(scope)
	if err != nil || len(rows) == 0 {
		return err
	}
	regular := []protocol.JSONObject{}
	shutdown := []protocol.JSONObject{}
	for _, row := range rows {
		kind, parsed := teamspawn.ParseInteractiveProtocol(row.String("text"))
		trusted := row.String("from") == "team-lead"
		switch kind {
		case "permission_response":
			if trusted {
				if future := m.teamWorker.permissions[parsed.String("request_id")]; future != nil {
					delete(m.teamWorker.permissions, parsed.String("request_id"))
					if err = future.Resolve(scope, parsed); err != nil {
						return err
					}
				}
			}
		case "plan_approval_response":
			if trusted && id.PlanModeRequired && string(parsed.Get("approved")) == "true" {
				mode := parsed.String("permissionMode")
				if mode == "" {
					mode = "default"
				}
				if err = m.Host.SetNativePermissionMode(scope, mode); err != nil {
					return err
				}
			}
			// The original poller also forwards plan responses as regular context.
			regular = append(regular, row)
		case "team_permission_update":
			if trusted {
				update, _ := parsed.Object("permissionUpdate")
				update = update.Clone()
				update.Set("destination", quoteMain("session"))
				update.Set("type", quoteMain("addRules"))
				if err = m.Host.ApplyNativePermissionUpdates(scope, rawMain([]protocol.JSONObject{update})); err != nil {
					return err
				}
			}
		case "mode_set_request":
			if trusted {
				mode := parsed.String("mode")
				if err = m.Host.SetNativePermissionMode(scope, mode); err != nil {
					return err
				}
				write, err := nativeSkillWork(m, scope, func() (struct{}, error) {
					_, err := config.Mutate(id.TeamName, func(team protocol.JSONObject) (protocol.JSONObject, error) {
						var members []protocol.JSONObject
						_ = json.Unmarshal(team.Get("members"), &members)
						for i := range members {
							if members[i].String("name") == id.AgentName {
								members[i].Set("mode", quoteMain(mode))
							}
						}
						team.Set("members", rawMain(members))
						return team, nil
					})
					return struct{}{}, err
				})
				if err != nil {
					return err
				}
				if _, err = write.Await(scope); err != nil {
					return err
				}
			}
		case "shutdown_request":
			shutdown = append(shutdown, row)
		case "permission_request", "sandbox_permission_request", "sandbox_permission_response", "plan_approval_request", "shutdown_approved":
		default:
			regular = append(regular, row)
		}
	}
	regular = append(regular, shutdown...)
	if err = m.Controller.QueueTeamInboxOnOwner(scope, regular); err != nil {
		return err
	}
	return nil
}
func (m *MainPrint) nativePanePermission(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, in *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
	if !scope.BelongsTo(m.Owner) || in == nil {
		return queryloop.ToolPermission{}, cooperative.ErrRestricted
	}
	mailbox, config, err := m.nativePaneMailbox(scope)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	id := *m.teamWorker.pane
	requestID := "perm-" + fmt.Sprint(int64(m.options.Native.NowMillis())) + "-" + uuidMain()[:7]
	future, err := ownerasync.NewOwnerFuture[protocol.JSONObject](m.Owner)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	if m.teamWorker.permissions == nil {
		m.teamWorker.permissions = map[string]*ownerasync.OwnerFuture[protocol.JSONObject]{}
	}
	m.teamWorker.permissions[requestID] = future
	settled := make(chan struct{})
	defer close(settled)
	go func() {
		select {
		case <-settled:
			return
		case <-ctx.Done():
			_, _ = inProcessParentCall(context.Background(), m, func(next *cooperative.Scope) (struct{}, error) {
				if m.teamWorker.permissions[requestID] == future {
					delete(m.teamWorker.permissions, requestID)
					return struct{}{}, future.Resolve(next, protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quoteMain("error")}))
				}
				return struct{}{}, nil
			})
		}
	}()
	defer delete(m.teamWorker.permissions, requestID)
	request := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("permission_request")}, protocol.JSONField{Name: "request_id", Value: quoteMain(requestID)}, protocol.JSONField{Name: "agent_id", Value: quoteMain(id.AgentName)}, protocol.JSONField{Name: "tool_name", Value: quoteMain(tool.Name)}, protocol.JSONField{Name: "tool_use_id", Value: call.ToolUseID}, protocol.JSONField{Name: "input", Value: rawMain(*in)})
	if call.PermDecision != nil {
		request.Set("description", call.PermDecision.Description)
		request.Set("permission_suggestions", call.PermDecision.Suggestions)
	}
	if request.Get("description") == nil {
		request.Set("description", quoteMain(""))
	}
	if request.Get("permission_suggestions") == nil {
		request.Set("permission_suggestions", rawMain([]any{}))
	}
	stamp := m.options.Native.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	write, err := nativeSkillWork(m, scope, func() (struct{}, error) {
		file := config.Read(id.TeamName)
		if file == nil {
			return struct{}{}, fmt.Errorf("Team file not found for team: %s", id.TeamName)
		}
		lead := "team-lead"
		var members []protocol.JSONObject
		_ = json.Unmarshal(file.Get("members"), &members)
		for _, row := range members {
			if row.String("agentId") == file.String("leadAgentId") {
				lead = row.String("name")
				break
			}
		}
		envelope := protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: quoteMain(id.AgentName)}, protocol.JSONField{Name: "text", Value: quoteMain(string(rawMain(request)))}, protocol.JSONField{Name: "timestamp", Value: quoteMain(stamp)})
		if id.Color != "" {
			envelope.Set("color", quoteMain(id.Color))
		}
		return struct{}{}, mailbox.Write(lead, id.TeamName, envelope)
	})
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	if _, err = write.Await(scope); err != nil {
		return queryloop.ToolPermission{}, err
	}
	response, err := future.Await(scope)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	decision := queryloop.ToolPermission{Input: in, Behavior: "deny", Message: response.Get("error")}
	if response.String("subtype") == "success" {
		decision.Behavior = "allow"
		if data, ok := response.Object("response"); ok {
			if updated, ok := data.Object("updated_input"); ok && len(updated.OwnProperties()) > 0 {
				decision.UpdatedInput = &updated
			}
			decision.UpdatedPermissions = data.Get("permission_updates")
		}
	}
	return decision, nil
}

func (m *MainPrint) nativePaneIdleOnOwner(scope *cooperative.Scope, history []protocol.JSONObject) error {
	mailbox, config, err := m.nativePaneMailbox(scope)
	if err != nil {
		return err
	}
	id := *m.teamWorker.pane
	summary, err := teamspawn.LastPeerDMSummary(history)
	if err != nil {
		return err
	}
	stamp := m.options.Native.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	notification := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("idle_notification")}, protocol.JSONField{Name: "from", Value: quoteMain(id.AgentName)}, protocol.JSONField{Name: "timestamp", Value: quoteMain(stamp)}, protocol.JSONField{Name: "idleReason", Value: quoteMain("available")}, protocol.JSONField{Name: "summary", Value: summary})
	write, err := nativeSkillWork(m, scope, func() (struct{}, error) {
		file := config.Read(id.TeamName)
		if file == nil {
			return struct{}{}, nil
		}
		leader := "team-lead"
		var members []protocol.JSONObject
		_ = json.Unmarshal(file.Get("members"), &members)
		for _, row := range members {
			if row.String("agentId") == file.String("leadAgentId") {
				leader = row.String("name")
			}
		}
		_, _ = config.Mutate(id.TeamName, func(team protocol.JSONObject) (protocol.JSONObject, error) {
			var members []protocol.JSONObject
			_ = json.Unmarshal(team.Get("members"), &members)
			for i := range members {
				if members[i].String("name") == id.AgentName {
					members[i].Set("isActive", rawMain(false))
				}
			}
			team.Set("members", rawMain(members))
			return team, nil
		})
		envelope := protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: quoteMain(id.AgentName)}, protocol.JSONField{Name: "text", Value: quoteMain(string(rawMain(notification)))}, protocol.JSONField{Name: "timestamp", Value: quoteMain(stamp)})
		if id.Color != "" {
			envelope.Set("color", quoteMain(id.Color))
		}
		return struct{}{}, mailbox.Write(leader, id.TeamName, envelope)
	})
	if err != nil {
		return err
	}
	_, err = write.Await(scope)
	return err
}

func (m *MainPrint) initializeNativePaneTeamPermissions(scope *cooperative.Scope) error {
	if m.teamWorker == nil || m.teamWorker.pane == nil {
		return nil
	}
	_, config, err := m.nativePaneMailbox(scope)
	if err != nil {
		return err
	}
	team := m.teamWorker.pane.TeamName
	work, err := nativeSkillWork(m, scope, func() (*protocol.JSONObject, error) { return config.Read(team), nil })
	if err != nil {
		return err
	}
	file, err := work.Await(scope)
	if err != nil || file == nil {
		return err
	}
	var paths []protocol.JSONObject
	_ = json.Unmarshal(file.Get("teamAllowedPaths"), &paths)
	updates := []protocol.JSONObject{}
	for _, path := range paths {
		content := path.String("path") + "/**"
		if strings.HasPrefix(path.String("path"), "/") {
			content = "/" + content
		}
		rule := protocol.NewJSONObject(protocol.JSONField{Name: "toolName", Value: path.Get("toolName")}, protocol.JSONField{Name: "ruleContent", Value: quoteMain(content)})
		updates = append(updates, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("addRules")}, protocol.JSONField{Name: "rules", Value: rawMain([]protocol.JSONObject{rule})}, protocol.JSONField{Name: "behavior", Value: quoteMain("allow")}, protocol.JSONField{Name: "destination", Value: quoteMain("session")}))
	}
	return m.Host.ApplyNativePermissionUpdates(scope, rawMain(updates))
}
func (m *MainPrint) markNativePaneActive(scope *cooperative.Scope) error {
	if m.teamWorker == nil || m.teamWorker.pane == nil {
		return nil
	}
	_, config, err := m.nativePaneMailbox(scope)
	if err != nil {
		return err
	}
	id := *m.teamWorker.pane
	work, err := nativeSkillWork(m, scope, func() (struct{}, error) {
		_, err := config.Mutate(id.TeamName, func(file protocol.JSONObject) (protocol.JSONObject, error) {
			var members []protocol.JSONObject
			_ = json.Unmarshal(file.Get("members"), &members)
			for i := range members {
				if members[i].String("name") == id.AgentName {
					members[i].Set("isActive", rawMain(true))
				}
			}
			file.Set("members", rawMain(members))
			return file, nil
		})
		return struct{}{}, err
	})
	if err != nil {
		return err
	}
	_, err = work.Then(scope, func(_ *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			_, _ = fmt.Fprintln(m.options.Error, failure)
		}
		return nil
	})
	return err
}
