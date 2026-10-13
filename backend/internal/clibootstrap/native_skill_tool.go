package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (m *MainPrint) nativeSkillCommands(scope *cooperative.Scope) ([]builtintools.SkillCommand, error) {
	if !scope.BelongsTo(m.Owner) || m.skillCommands == nil {
		return nil, cooperative.ErrRestricted
	}
	definitions, _, e := m.nativeCommandDefinitions(context.Background(), scope)
	if e != nil {
		return nil, e
	}
	commands := []builtintools.SkillCommand{}
	for _, definition := range definitions {
		copy := definition
		commands = append(commands, builtintools.SkillCommand{Metadata: copy.Metadata(), Definition: &copy})
	}
	for _, command := range m.Registry.ModelSkillCommands() {
		present := false
		for _, definition := range definitions {
			present = present || definition.Name() == command.Name || definition.UserFacingName() == command.Name
		}
		if present {
			continue
		}
		commands = append(commands, builtintools.SkillCommand{Metadata: protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain(command.Name)}, protocol.JSONField{Name: "type", Value: quoteMain("local")})})
	}
	return commands, nil
}
func (m *MainPrint) nativeSkillBindings() *builtintools.SkillBindings {
	m.ensureNativeSkillState()
	return &builtintools.SkillBindings{Commands: m.nativeSkillCommands, Call: m.nativeSkillCall}
}
func (m *MainPrint) nativeSkillCall(ctx context.Context, scope *cooperative.Scope, name string, command builtintools.SkillCommand, args *string, call queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
	if command.Definition == nil {
		return queryloop.ToolCallResult{}, errors.New("Command processing failed")
	}
	nameJSON := command.InvocationNameJSON
	if nameJSON == nil {
		nameJSON = quoteMain(name)
	}
	if e := m.recordNativeSkillUsageJSON(scope, nameJSON); e != nil {
		return queryloop.ToolCallResult{}, e
	}
	meta := command.Metadata
	actual, ok := call.Opaque.(*builtinwrite.CallContext)
	if !ok || actual == nil {
		return queryloop.ToolCallResult{}, cooperative.ErrRestricted
	}
	actualArgs := ""
	if args != nil {
		actualArgs = *args
	}
	processed, e := m.nativeSkillPrompt(ctx, scope, *command.Definition, actualArgs, actual.Messages, nil)
	if e != nil {
		return queryloop.ToolCallResult{}, e
	}
	allowed := processed.CommandRules
	data := protocol.NewJSONObject(protocol.JSONField{Name: "success", Value: json.RawMessage("true")}, protocol.JSONField{Name: "commandName", Value: nameJSON})
	if len(allowed) > 0 {
		data.Set("allowedTools", rawMain(allowed))
	}
	if model := meta.Get("model"); model != nil {
		data.Set("model", model)
	}
	newMessages := []queryloop.Message{}
	for _, message := range processed.Messages {
		if message.String("type") == "user" {
			var api protocol.JSONObject
			_ = api.UnmarshalJSON(message.Get("message"))
			if content := api.Get("content"); len(content) > 0 && content[0] == '"' {
				var text string
				_ = json.Unmarshal(content, &text)
				if strings.Contains(text, "<command-message>") {
					continue
				}
			}
			if call.Invocation.Assistant != nil {
				for _, block := range call.Invocation.Assistant.Content {
					if block.Object.String("type") == "tool_use" && block.Object.String("name") == "Skill" {
						if planRawTruthy(block.Object.Get("id")) {
							message.Set("sourceToolUseID", block.Object.Get("id"))
						}
						break
					}
				}
			}
		}
		newMessages = append(newMessages, queryloop.Message{Record: message})
	}
	return queryloop.ToolCallResult{Data: rawMain(data), NewMessages: newMessages, ModifyContext: func(previous any) (any, error) {
		current, ok := previous.(*builtinwrite.CallContext)
		if !ok {
			return nil, cooperative.ErrRestricted
		}
		next := *current
		overrides := protocol.NewJSONObject()
		if current.SkillOverrides != nil {
			overrides = current.SkillOverrides.Clone()
		}
		var rules []json.RawMessage
		_ = json.Unmarshal(overrides.Get("commandRules"), &rules)
		for _, rule := range allowed {
			exists := false
			for _, old := range rules {
				exists = exists || planSourcesSame(old, rule)
			}
			if !exists {
				rules = append(rules, append(json.RawMessage(nil), rule...))
			}
		}
		if len(allowed) > 0 {
			overrides.Set("commandRules", rawMain(rules))
		}
		if model := meta.Get("model"); model != nil {
			resolved, e := m.nativeSkillModel(scope, meta.String("model"), current.MainLoopModel)
			if e != nil {
				return nil, e
			}
			overrides.Set("model", quoteMain(resolved))
		}
		if effort := meta.Get("effort"); effort != nil {
			overrides.Set("effort", effort)
		}
		next.SkillOverrides = &overrides
		return &next, nil
	}}, nil
}
func rawMain(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func planSourcesSame(a, b json.RawMessage) bool {
	au, e := protocol.DecodeJavaScriptJSONString(a)
	if e != nil {
		return false
	}
	bu, e := protocol.DecodeJavaScriptJSONString(b)
	if e != nil || len(au) != len(bu) {
		return false
	}
	for i := range au {
		if au[i] != bu[i] {
			return false
		}
	}
	return true
}
