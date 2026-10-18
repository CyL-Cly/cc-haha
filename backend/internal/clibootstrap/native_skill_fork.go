package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// Source's direct runAgent(isAsync:false) does not install AgentTool's
// background-promotion action. Keep the actual foreground child producer;
// this profile changes only that action and the skill-local permission view.
type nativeSkillForkProfile struct {
	rules     []json.RawMessage
	agentType string
	progress  func(*cooperative.Scope, string, string, json.RawMessage) error
}

func (p *nativeSkillForkProfile) bind(o *MainPrintOptions) error {
	if p == nil || o.DetachedAgent == nil || o.DetachedAgent.liveSettings == nil || o.DetachedAgent.transferSettings == nil {
		return cooperative.ErrRestricted
	}
	d := o.DetachedAgent
	d.AgentType = p.agentType
	live, transfer := d.liveSettings, d.transferSettings
	rules := append([]json.RawMessage(nil), p.rules...)
	d.liveSettings = func(child *MainPrint, scope *cooperative.Scope) (*protocol.JSONObject, protocol.JSONObject, error) {
		settings, permission, err := live(child, scope)
		if err != nil {
			return nil, protocol.JSONObject{}, err
		}
		permission, err = sdkhost.NativeSkillCommandPermission(permission, rules)
		return settings, permission, err
	}
	d.transferSettings = func(child *MainPrint, scope *cooperative.Scope) (sdkhost.NativeSettingsTransfer, error) {
		value, err := transfer(child, scope)
		if err != nil {
			return sdkhost.NativeSettingsTransfer{}, err
		}
		return value.WithCommandRules(rules)
	}
	// Both Source fork entrypoints pass this value explicitly, including for
	// the built-in general-purpose definition.
	o.QuerySource = "agent:custom"
	return nil
}

func (m *MainPrint) nativeRunForkedSkill(ctx context.Context, scope *cooperative.Scope, command skillcommands.Definition, args string, progress ...func(*cooperative.Scope, string, string, json.RawMessage) error) (builtintools.AgentRunResult, error) {
	var zero builtintools.AgentRunResult
	if !scope.BelongsTo(m.Owner) || m.agents == nil || m.Host == nil {
		return zero, cooperative.ErrRestricted
	}
	invocation, err := m.nativeSkillInvocation(scope)
	if err != nil {
		return zero, err
	}
	blocks, err := command.PromptBlocks(ctx, &args, invocation)
	if err != nil {
		return zero, err
	}
	meta := command.Metadata()
	allowed, err := skillcommands.ParseInvocationTools(meta.Get("allowedTools"))
	if err != nil {
		return zero, err
	}
	definitions, err := m.agents.List(scope)
	if err != nil {
		return zero, err
	}
	// prepareForkedCommandContext: requested agent, general-purpose, then the
	// first active definition. An unavailable optional agent name is a fallback.
	wanted := meta.String("agent")
	if wanted == "" {
		wanted = "general-purpose"
	}
	var selected *agentcatalog.Definition
	for _, name := range []string{wanted, "general-purpose"} {
		for _, definition := range definitions.Active {
			if definition.Name == name {
				copy := definition
				selected = &copy
				break
			}
		}
		if selected != nil {
			break
		}
	}
	if selected == nil && len(definitions.Active) > 0 {
		copy := definitions.Active[0]
		selected = &copy
	}
	if selected == nil {
		return zero, errors.New("No agent available for forked execution")
	}
	available, err := m.Host.NativeSkillForkToolNames(scope)
	if err != nil {
		return zero, err
	}
	resolved := agentcatalog.ResolveTools(*selected, available, false, false)
	prompt, err := m.nativeAgentPromptJSON(scope, *selected)
	if err != nil {
		return zero, err
	}
	permission, err := m.nativeToolPermissionContext(scope)
	if err != nil {
		return zero, err
	}
	permission, err = sdkhost.NativeSkillCommandPermission(permission, allowed)
	if err != nil {
		return zero, err
	}
	mode, override := permission.String("mode"), selected.Fields.String("permissionMode")
	if (selected.Source == "projectSettings" || selected.Source == "localSettings") && override == "bypassPermissions" {
		override = ""
	}
	parentModel, err := m.Host.InferenceModel(scope)
	if err != nil {
		return zero, err
	}
	effort := selected.Effort
	if value := meta.Get("effort"); value != nil {
		effort = value
	}
	var maxTurns *float64
	if raw := selected.Fields.Get("maxTurns"); raw != nil {
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			return zero, err
		}
		maxTurns = &value
	}
	request := builtintools.AgentRequest{Prompt: skillcommands.PromptText(blocks), Description: command.Name(), AgentType: selected.Name, Model: meta.String("model"), DefinitionModel: selected.Model, Source: selected.Source, SelectedAgent: selected.Fields.Clone(), Effort: append(json.RawMessage(nil), effort...), MaxTurns: maxTurns, OmitClaudeMd: string(selected.Fields.Get("omitClaudeMd")) == "true", SystemPromptSections: []json.RawMessage{append(json.RawMessage(nil), prompt...), quoteMain(workflowAgentNotes)}, PermissionContext: permission, ParentPermissionMode: mode, PermissionModeOverride: override, Tools: resolved.ResolvedTools, ParentTools: append([]string(nil), available...), ParentModel: parentModel}
	if len(progress) > 1 {
		return zero, cooperative.ErrRestricted
	}
	profile := &nativeSkillForkProfile{rules: allowed, agentType: selected.Name}
	if len(progress) == 1 {
		profile.progress = progress[0]
	}
	future, err := m.startNativeForegroundAgent(ctx, scope, request, profile)
	if err != nil {
		return zero, err
	}
	result, err := future.Await(scope)
	if err != nil {
		return zero, err
	}
	if result.Async {
		return zero, errors.New("forked Skill unexpectedly promoted")
	}
	return result, nil
}

// extractResultText uses only the last assistant, including an empty last
// assistant. It must not reuse an earlier text when the last message is tool-only.
func nativeForkSkillResultText(rows []queryloop.Message, fallback string) string {
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Type() != "assistant" {
			continue
		}
		data, err := rows[i].MarshalJSON()
		if err != nil {
			return fallback
		}
		var envelope protocol.JSONObject
		if envelope.UnmarshalJSON(data) != nil {
			return fallback
		}
		message, _ := envelope.Object("message")
		var blocks []protocol.JSONObject
		if json.Unmarshal(message.Get("content"), &blocks) != nil {
			return fallback
		}
		texts := []string{}
		for _, block := range blocks {
			if block.String("type") == "text" {
				texts = append(texts, block.String("text"))
			}
		}
		if text := strings.Join(texts, "\n"); text != "" {
			return text
		}
		return fallback
	}
	return fallback
}

// Source processSlashCommand catches a fork's error and keeps the command
// local. SkillTool intentionally retains the original rejection instead.
func (m *MainPrint) nativeForkSkillSlashFailure(command skillcommands.Definition, args string, failure error) (*sessioncontroller.ProcessedInput, error) {
	if errors.Is(failure, cooperative.ErrClosed) || errors.Is(failure, cooperative.ErrRestricted) {
		return nil, failure
	}
	var malformed *skillcommands.MalformedCommandError
	if errors.As(failure, &malformed) {
		return nil, failure
	}
	name := command.UserFacingName()
	if name == "" {
		name = command.Name()
	}
	input := "<command-name>/" + name + "</command-name>\n            <command-message>" + name + "</command-message>\n            <command-args>" + args + "</command-args>"
	first := messages.OrderedUser(m.factory.CreateUser(storage.Object{"content": quoteMain(input)}))
	var second protocol.JSONObject
	if errors.Is(failure, context.Canceled) {
		second = messages.OrderedUser(m.factory.CreateUserInterruption(false))
	} else {
		text := failure.Error()
		if !strings.HasPrefix(text, "Error:") {
			text = "Error: " + text
		}
		second = messages.OrderedUser(m.factory.CreateUser(storage.Object{"content": quoteMain("<local-command-stderr>" + text + "</local-command-stderr>")}))
	}
	return &sessioncontroller.ProcessedInput{Messages: []protocol.JSONObject{first, second}, NoQuery: true, AttachmentsHandled: true, StartMillis: m.options.Native.NowMillis()}, nil
}

// SkillTool's actual onProgress contract is separate from AgentTool's routing.
func (m *MainPrint) nativeForkSkillProgress(call queryloop.JSONToolCallContext) func(*cooperative.Scope, string, string, json.RawMessage) error {
	if call.OnProgress == nil || call.Invocation.Assistant == nil {
		return nil
	}
	childID := quoteMain("skill_" + call.Invocation.Assistant.Message.String("id"))
	return func(scope *cooperative.Scope, agentID, prompt string, data json.RawMessage) error {
		var row protocol.JSONObject
		if err := row.UnmarshalJSON(data); err != nil {
			return err
		}
		if row.String("type") != "assistant" && row.String("type") != "user" {
			return nil
		}
		// OnQueryYield supplies the original internal envelope before the child's
		// SDK normalizer. Source normalizes it here before selecting tool blocks.
		normalized, err := m.Host.NativeSkillProgressMessages(scope, data)
		if err != nil {
			return err
		}
		for _, current := range normalized {
			message, _ := current.Object("message")
			var blocks []protocol.JSONObject
			if err := json.Unmarshal(message.Get("content"), &blocks); err != nil {
				return err
			}
			for _, block := range blocks {
				if kind := block.String("type"); kind == "tool_use" || kind == "tool_result" {
					if err := call.OnProgress(childID, protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: rawMain(current)}, protocol.JSONField{Name: "type", Value: quoteMain("skill_progress")}, protocol.JSONField{Name: "prompt", Value: quoteMain(prompt)}, protocol.JSONField{Name: "agentId", Value: quoteMain(agentID)})); err != nil {
						return err
					}
					break
				}
			}
		}
		return nil
	}
}
