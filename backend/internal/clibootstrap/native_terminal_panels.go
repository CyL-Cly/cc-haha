package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

// Terminal snapshots and edits run on the existing Harness owner. Panels never
// maintain a parallel task/permission/MCP configuration or invoke a model.
func (m *MainPrint) TerminalPanelSnapshot(ctx context.Context, kind string) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.Host == nil {
			return cooperative.ErrClosed
		}
		var err error
		switch kind {
		case "permissions":
			result, err = m.Host.NativePermissionContext(scope)
			result.Set("originalWorkingDirectory", quoteMain(m.state.Snapshot().OriginalCWD))
		case "tasks":
			var tasks []protocol.JSONObject
			tasks, err = m.Controller.BackgroundTasksOnOwner(scope)
			result = protocol.NewJSONObject(protocol.JSONField{Name: "tasks", Value: rawMain(tasks)})
		case "todos":
			if m.options.Interactive || pickerTrue(m.options.Native.Environment["CLAUDE_CODE_ENABLE_TASKS"]) {
				list, e := m.Controller.TaskListIDOnOwner(scope, m.options.AgentID)
				if e != nil {
					return e
				}
				tasks, e := m.taskStore.List(list)
				if e != nil {
					return e
				}
				result = protocol.NewJSONObject(protocol.JSONField{Name: "tasks", Value: rawMain(tasks)})
			} else {
				values, e := m.Controller.GetTodosOnOwner(scope)
				if e != nil {
					return e
				}
				result = protocol.NewJSONObject(protocol.JSONField{Name: "tasks", Value: values})
			}
		case "skills":
			result = protocol.NewJSONObject(protocol.JSONField{Name: "skills", Value: rawMain(m.Registry.TerminalSkillMetadata())})
		case "mcp":
			var servers []protocol.JSONObject
			servers, err = m.mcpTools.ServerStatuses(ctx, scope)
			result = protocol.NewJSONObject(protocol.JSONField{Name: "servers", Value: rawMain(servers)})
		default:
			return errors.New("Unknown terminal panel: " + kind)
		}
		result = result.Clone()
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
func (m *MainPrint) TerminalPermissionRule(ctx context.Context, kind, behavior, source, text string) error {
	if kind != "addRules" && kind != "removeRules" {
		return errors.New("Invalid permission edit")
	}
	if behavior != "allow" && behavior != "ask" && behavior != "deny" {
		return errors.New("Invalid permission behavior")
	}
	if source != "userSettings" && source != "projectSettings" && source != "localSettings" && source != "session" {
		return errors.New("Permission source is read-only")
	}
	parsed, err := clisettings.ValidateNativeJSONContent(string(rawMain(map[string]any{"permissions": map[string]any{behavior: []string{text}}})), "terminal permission rule")
	if err != nil {
		return err
	}
	if parsed == nil || len(parsed.Errors) > 0 {
		return errors.New("Invalid permission rule")
	}
	rule, err := sdkpermissions.ParsePermissionRuleValue(quoteMain(text))
	if err != nil {
		return err
	}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed {
			return cooperative.ErrClosed
		}
		policy, err := m.Startup.GetSettingsForSource("policySettings")
		if err != nil {
			return err
		}
		if policy != nil && string(policy.Get("allowManagedPermissionRulesOnly")) == "true" {
			return errors.New("Permission rules are managed by your organization")
		}
		if err := m.Host.ApplyNativePermissionUpdates(scope, rawMain([]any{map[string]any{"type": kind, "behavior": behavior, "destination": source, "rules": []protocol.JSONObject{rule}}})); err != nil {
			return err
		}
		return m.Host.RefreshNativeToolCatalog(scope)
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
func (m *MainPrint) TerminalMCPControl(ctx context.Context, name string, enabled *bool) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed {
			return cooperative.ErrClosed
		}
		request := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quoteMain("mcp_reconnect")}, protocol.JSONField{Name: "serverName", Value: quoteMain(name)})
		if enabled != nil {
			request.Set("subtype", quoteMain("mcp_toggle"))
			request.Set("enabled", rawMain(*enabled))
		}
		_, err := m.nativeSDKMCPControl(scope, request)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
func (m *MainPrint) TerminalTaskOutput(ctx context.Context, id string) (string, error) {
	var read func(context.Context) (json.RawMessage, error)
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		task, err := m.Controller.BackgroundTaskOnOwner(scope, id)
		if err != nil {
			return err
		}
		if task == nil {
			return errors.New("Task not found: " + id)
		}
		read = task.ReadOutput
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	if err != nil {
		return "", err
	}
	value, err := read(ctx)
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text, err
	}
	return string(value), err
}
func (m *MainPrint) StopTerminalAgents(ctx context.Context) (int, error) {
	value, err := m.TerminalPanelSnapshot(ctx, "tasks")
	if err != nil {
		return 0, err
	}
	var tasks []protocol.JSONObject
	_ = json.Unmarshal(value.Get("tasks"), &tasks)
	count := 0
	for _, task := range tasks {
		if task.String("type") == "local_agent" && task.String("status") == "running" {
			if _, err = m.StopBackgroundTask(ctx, task.String("id")); err != nil {
				return count, err
			}
			count++
		}
	}
	return count, nil
}
