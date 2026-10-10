package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (m *MainPrint) nativeHookCommandBindings() *queryhooks.CommandBindings {
	if !m.options.Native.NativeProcess {
		return nil
	}
	bindings := &queryhooks.CommandBindings{Async: m.asyncHooks, Owner: m.Owner, Factory: m.factory, Timestamp: func() string { return m.options.Native.Now().UTC().Format("2006-01-02T15:04:05.000Z") }, NowMillis: m.options.Native.NowMillis}
	bindings.ShellResolver = queryhooks.NewCommandShellResolver(m.options.Native.Environment, m.options.Platform)
	if d := m.options.DetachedAgent; d != nil && d.preset != nil {
		bindings.Store = d.preset.parent.hook.NativeCommandStore()
		bindings.ShellResolver = d.preset.parent.hook.NativeCommandShellResolver()
	}
	bindings.Input = m.nativeStopHookInput
	bindings.Event = m.nativeHookSDKEvent
	bindings.Run = func(ctx context.Context, call queryhooks.Context, hook, input protocol.JSONObject) (queryhooks.CommandOutput, error) {
		if call.Scope == nil || !call.Scope.BelongsTo(m.Owner) {
			return queryhooks.CommandOutput{}, cooperative.ErrRestricted
		}
		n := m.options.Native
		state := m.state.Snapshot()
		if e := n.check("command-hook-cwd", state.CWD); e != nil {
			return queryhooks.CommandOutput{}, e
		}
		environment := map[string]string{}
		for key, value := range m.nativeOpenAIEnvironment() {
			environment[key] = value
		}
		sid := m.Controller.SessionID()
		if d := m.options.DetachedAgent; d != nil && d.preset != nil {
			var err error
			sid, err = nativeAgentOwnerCall(m, call.Scope, d.preset.parent, func(*cooperative.Scope) (string, error) { return d.preset.parent.Controller.SessionID(), nil })
			if err != nil {
				return queryhooks.CommandOutput{}, err
			}
		}
		progress := func(out queryhooks.CommandOutput) error {
			receipt, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
				current := call
				current.Scope = scope
				return m.nativeHookSDKEvent(current, "progress", call.HookID, call.HookEvent, out, "")
			})
			if e == nil {
				e = receipt.Wait()
			}
			return e
		}
		directory := ""
		if m.asyncHooks != nil {
			var err error
			directory, err = m.nativeTaskOutputDirectory(call.Scope)
			if err != nil {
				return queryhooks.CommandOutput{}, err
			}
		}
		future, e := nativeSkillWork(m, call.Scope, func() (queryhooks.CommandOutput, error) {
			if m.hookEnvironment != nil && hook.String("shell") != "powershell" && call.HookIndex != nil {
				switch call.HookEvent {
				case "SessionStart", "Setup", "CwdChanged", "FileChanged":
					path, err := m.hookEnvironment.HookEnvFile(sid, call.HookEvent, *call.HookIndex)
					if err != nil {
						return queryhooks.CommandOutput{}, err
					}
					environment["CLAUDE_ENV_FILE"] = path
				}
			}
			return queryhooks.RunCommandProcess(ctx, queryhooks.CommandProcessRequest{AllowAsync: m.asyncHooks != nil, OutputDirectory: directory, ShellResolver: bindings.ShellResolver, Progress: progress, Hook: hook, Input: input, CWD: state.CWD, OriginalCWD: state.OriginalCWD, ProjectRoot: state.ProjectRoot, Platform: m.options.Platform, Environment: environment, NowMillis: n.NowMillis})
		})
		if e != nil {
			return queryhooks.CommandOutput{}, e
		}
		out, e := future.Await(call.Scope)
		if e == nil {
			e = m.registerNativeAsyncHook(call, hook, out)
		}
		return out, e
	}
	return bindings
}

func (m *MainPrint) nativeHookSDKEvent(call queryhooks.Context, kind, id, event string, out queryhooks.CommandOutput, _ string) error {
	if call.Scope == nil || !call.Scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if d := m.options.DetachedAgent; d != nil && d.preset != nil {
		return bridgeAgentVoid(m, d.preset.parent, call.Scope, func(scope *cooperative.Scope) error {
			current := call
			current.Scope = scope
			return d.preset.parent.nativeHookSDKEvent(current, kind, id, event, out, "")
		})
	}
	if !m.options.HookEvents {
		return nil
	}
	if event != "SessionStart" && event != "Setup" && !m.options.Arguments.IncludeHookEvents && !skillTruthy(m.options.Native.Environment["CLAUDE_CODE_REMOTE"]) {
		return nil
	}
	subtype := "hook_response"
	if kind == "started" {
		subtype = "hook_started"
	}
	if kind == "progress" {
		subtype = "hook_progress"
	}
	message := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain(subtype)}, protocol.JSONField{Name: "hook_id", Value: quoteMain(id)}, protocol.JSONField{Name: "hook_name", Value: quoteMain(event)}, protocol.JSONField{Name: "hook_event", Value: quoteMain(event)})
	if call.HookName != "" {
		message.Set("hook_name", quoteMain(call.HookName))
	}
	if kind != "started" {
		if kind == "progress" {
			message.Set("stdout", quoteMain(out.Stdout))
			message.Set("stderr", quoteMain(out.Stderr))
			message.Set("output", quoteMain(out.Output))
		} else {
			message.Set("output", quoteMain(out.Output))
			message.Set("stdout", quoteMain(out.Stdout))
			message.Set("stderr", quoteMain(out.Stderr))
			if !out.CodeAbsent {
				message.Set("exit_code", rawMain(out.Code))
			}
			message.Set("outcome", quoteMain(kind))
		}
	}
	message.Set("uuid", quoteMain(uuidMain()))
	message.Set("session_id", quoteMain(m.Controller.SessionID()))
	data, e := protocol.EncodeNDJSON(message)
	if e != nil {
		return e
	}
	return m.enqueue(data)
}

func (m *MainPrint) nativeStopHookInput(ctx context.Context, call queryhooks.Context) (protocol.JSONObject, error) {
	if call.Scope == nil || !call.Scope.BelongsTo(m.Owner) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	permission, e := m.nativeToolPermissionContext(call.Scope)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	path, e := m.Controller.Writer().ComputedTranscriptPath(call.Scope)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	mainPath := path
	if d := m.options.DetachedAgent; d != nil && d.preset != nil {
		mainPath, e = nativeAgentOwnerCall(m, call.Scope, d.preset.parent, func(scope *cooperative.Scope) (string, error) {
			return d.preset.parent.Controller.Writer().ComputedTranscriptPath(scope)
		})
		if e != nil {
			return protocol.JSONObject{}, e
		}
	}
	state := m.state.Snapshot()
	input := protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "transcript_path", Value: quoteMain(mainPath)}, protocol.JSONField{Name: "cwd", Value: quoteMain(state.CWD)}, protocol.JSONField{Name: "permission_mode", Value: quoteMain(permission.String("mode"))})
	if call.AgentID == "" && m.teamWorker != nil {
		input.Set("agent_type", quoteMain(m.teamWorker.preset.AgentType))
	} else if call.AgentID == "" && m.nativeMainThreadAgentType() != "" {
		input.Set("agent_type", quoteMain(m.nativeMainThreadAgentType()))
	}
	event := "Stop"
	if call.AgentID != "" {
		event = "SubagentStop"
	}
	if call.HookEvent != "" {
		event = call.HookEvent
	}
	// Task/idle wrappers call createBaseHookInput without child agentInfo;
	// the actual main-thread type remains part of the base, before event fields.
	taskEvent := event == "TaskCreated" || event == "TaskCompleted" || event == "TeammateIdle"
	baseOnlyEvent := taskEvent || event == "UserPromptSubmit"
	if taskEvent {
		input.Set("agent_type", call.InputFields.Get("agent_type"))
	}
	input.Set("hook_event_name", quoteMain(event))
	if event != "Stop" && event != "SubagentStop" {
		if call.AgentID != "" && !baseOnlyEvent {
			input.Set("agent_id", quoteMain(call.AgentID))
			if m.options.DetachedAgent != nil {
				input.Set("agent_type", quoteMain(m.options.DetachedAgent.AgentType))
			}
		}
		for _, field := range call.InputFields.OwnProperties() {
			_ = input.SetPropertyJSON(field.NameJSON, field.Value)
		}
		return input, context.Cause(ctx)
	}
	input.Set("stop_hook_active", rawMain(call.StopHookActive))
	if call.AgentID != "" {
		input.Set("agent_id", quoteMain(call.AgentID))
		agentPath := path
		if call.QuerySource == "side_question" && call.Scope.IsNativeSideQuestion(m.Owner) {
			agentPath, e = m.nativeAgentHookTranscript(call.Scope, call)
			if e != nil {
				return protocol.JSONObject{}, e
			}
		}
		input.Set("agent_transcript_path", quoteMain(agentPath))
		agentType := ""
		if m.options.DetachedAgent != nil {
			agentType = m.options.DetachedAgent.AgentType
		}
		input.Set("agent_type", quoteMain(agentType))
	}
	if text := nativeHookLastAssistant(call); text != nil {
		input.Set("last_assistant_message", text)
	}
	return input, context.Cause(ctx)
}

func nativeHookLastAssistant(call queryhooks.Context) json.RawMessage {
	history := append([]queryloop.Message{}, call.History...)
	for _, assistant := range call.Assistants {
		history = append(history, queryloop.Message{Assistant: assistant})
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Type() != "assistant" {
			continue
		}
		raw, e := history[i].MarshalJSON()
		if e != nil {
			return nil
		}
		var record protocol.JSONObject
		_ = record.UnmarshalJSON(raw)
		api, _ := record.Object("message")
		var content []protocol.JSONObject
		if json.Unmarshal(api.Get("content"), &content) != nil {
			return nil
		}
		units := []uint16{}
		first := true
		for _, block := range content {
			if block.String("type") != "text" {
				continue
			}
			text, e := protocol.DecodeJavaScriptJSONString(block.Get("text"))
			if e != nil {
				continue
			}
			if !first {
				units = append(units, '\n')
			}
			first = false
			units = append(units, text...)
		}
		for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
			units = units[1:]
		}
		for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
			units = units[:len(units)-1]
		}
		if len(units) == 0 {
			return nil
		}
		return protocol.QuoteJavaScriptUTF16(units)
	}
	return nil
}
