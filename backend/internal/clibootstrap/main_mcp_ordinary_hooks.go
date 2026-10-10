package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"path/filepath"
	"strings"
	"time"
)

func (m *NativeMCPToolContext) ordinaryHooks() (*queryhooks.Service, error) {
	c, n := m.command, m.command.Native
	factory, e := messages.NewFactory(uuidMain, n.Now)
	if e != nil {
		return nil, e
	}
	shell := queryhooks.NewCommandShellResolver(n.Environment, m.platform)
	bindings := &queryhooks.CommandBindings{Owner: c.Owner, Factory: factory, Timestamp: func() string { return n.Now().UTC().Format("2006-01-02T15:04:05.000Z") }, NowMillis: n.NowMillis, ShellResolver: shell,
		Input: func(ctx context.Context, call queryhooks.Context) (protocol.JSONObject, error) {
			if e := m.checkOrdinary(call.Scope); e != nil {
				return protocol.JSONObject{}, e
			}
			state := m.state.Snapshot()
			config := n.Environment["CLAUDE_CONFIG_DIR"]
			if config == "" {
				config = filepath.Join(n.Home, ".claude")
			}
			input := protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: quoteMain(state.SessionID)}, protocol.JSONField{Name: "transcript_path", Value: quoteMain(filepath.Join(config, "projects", projectpath.Sanitize(state.OriginalCWD), state.SessionID+".jsonl"))}, protocol.JSONField{Name: "cwd", Value: quoteMain(state.CWD)}, protocol.JSONField{Name: "hook_event_name", Value: quoteMain(call.HookEvent)})
			for _, field := range call.InputFields.OwnProperties() {
				_ = input.SetPropertyJSON(field.NameJSON, field.Value)
			}
			return input, context.Cause(ctx)
		},
		Run: func(ctx context.Context, call queryhooks.Context, hook, input protocol.JSONObject) (queryhooks.CommandOutput, error) {
			if e := m.checkOrdinary(call.Scope); e != nil {
				return queryhooks.CommandOutput{}, e
			}
			state := m.state.Snapshot()
			future, e := nativeOwnedWork(c.Owner, m.binding, call.Scope, func() (queryhooks.CommandOutput, error) {
				return queryhooks.RunCommandProcess(ctx, queryhooks.CommandProcessRequest{Hook: hook, Input: input, CWD: state.CWD, OriginalCWD: state.OriginalCWD, ProjectRoot: state.ProjectRoot, Platform: m.platform, Environment: n.Environment, NowMillis: n.NowMillis, ShellResolver: shell})
			})
			if e != nil {
				return queryhooks.CommandOutput{}, e
			}
			return future.Await(call.Scope)
		}}
	transport := &MainPrint{Owner: c.Owner, Startup: c.Startup, environment: c.Settings.environment, tls: m.modelTLS, commandBinding: m.binding, options: MainPrintOptions{Native: n, Platform: m.platform}}
	return queryhooks.New(queryhooks.Options{Commands: bindings, HTTP: transport.nativeHTTPHookBindings(), MergedSettings: c.Startup.GetInitialSettings, PolicySettings: func() (*protocol.JSONObject, error) { return c.Startup.GetSettingsForSource("policySettings") }, Profile: queryhooks.ResolvedSettingsProfile, Settings: c.Settings.settings, SettingsState: c.Settings.settingsState, MainSessionID: m.state.Snapshot().SessionID, PluginBaseAbsent: true, UUID: uuidMain})
}
func (m *NativeMCPToolContext) executeOrdinaryWorktreeHook(ctx context.Context, event string, fields protocol.JSONObject) ([]queryhooks.OutsideResult, error) {
	return ordinaryWorkflowOwner(m, ctx, func(scope *cooperative.Scope) ([]queryhooks.OutsideResult, error) {
		return m.hooks.ExecuteOutside(ctx, queryhooks.OutsideInput{Event: event, SessionID: m.state.Snapshot().SessionID, Fields: fields, Call: queryhooks.Context{Scope: scope}, Timeout: 10 * time.Minute})
	})
}
func (m *NativeMCPToolContext) ordinaryCreateHook(ctx context.Context, name string) (string, error) {
	rows, e := m.executeOrdinaryWorktreeHook(ctx, "WorktreeCreate", protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain(name)}))
	if e != nil {
		return "", e
	}
	failed := []string{}
	for _, row := range rows {
		var output, command string
		_ = json.Unmarshal(row.Output, &output)
		_ = json.Unmarshal(row.Command, &command)
		trimmed := strings.TrimFunc(output, protocol.IsJavaScriptWhitespace)
		if row.Succeeded && trimmed != "" {
			return trimmed, nil
		}
		if !row.Succeeded {
			if trimmed == "" {
				trimmed = "no output"
			}
			failed = append(failed, command+": "+trimmed)
		}
	}
	text := strings.Join(failed, "; ")
	if text == "" {
		text = "no successful output"
	}
	return "", fmt.Errorf("WorktreeCreate hook failed: %s", text)
}
func (m *NativeMCPToolContext) ordinaryRemoveHook(ctx context.Context, path string) (bool, error) {
	has, e := m.hooks.OutsideEventConfigured("WorktreeRemove", m.state.Snapshot().SessionID)
	if e != nil || !has {
		return false, e
	}
	rows, e := m.executeOrdinaryWorktreeHook(ctx, "WorktreeRemove", protocol.NewJSONObject(protocol.JSONField{Name: "worktree_path", Value: quoteMain(path)}))
	return len(rows) > 0, e
}
