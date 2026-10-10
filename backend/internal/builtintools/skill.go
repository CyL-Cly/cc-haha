package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

type SkillCommand struct {
	Metadata   protocol.JSONObject
	Definition *skillcommands.Definition
	// InvocationNameJSON is the current normalized input identity, not the
	// display/path name. It is private transient data carried to the executor.
	InvocationNameJSON json.RawMessage `json:"-"`
}
type SkillBindings struct {
	Commands func(*cooperative.Scope) ([]SkillCommand, error)
	Call     func(context.Context, *cooperative.Scope, string, SkillCommand, *string, queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error)
}

func (s *Service) HasSkillBindings() bool {
	return s != nil && s.options.Skill != nil && s.options.Skill.Commands != nil && s.options.Skill.Call != nil
}
func skillNameJSON(raw json.RawMessage) (json.RawMessage, error) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return nil, err
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
		units = units[1:]
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
		units = units[:len(units)-1]
	}
	if len(units) > 0 && units[0] == '/' {
		units = units[1:]
	}
	return protocol.QuoteJavaScriptUTF16(units), nil
}
func skillDisplayName(raw json.RawMessage) string {
	units, _ := protocol.DecodeJavaScriptJSONString(raw)
	return string(utf16.Decode(units))
}
func skillTrim(value string) string { return protocol.TrimJavaScriptString(value) }
func skillSameName(a, b json.RawMessage) bool {
	au, e := protocol.DecodeJavaScriptJSONString(a)
	if e != nil {
		return false
	}
	bu, e := protocol.DecodeJavaScriptJSONString(b)
	if e != nil {
		return false
	}
	return string(protocol.QuoteJavaScriptUTF16(au)) == string(protocol.QuoteJavaScriptUTF16(bu))
}
func skillNameMessage(prefix string, name json.RawMessage, suffix string) json.RawMessage {
	// name has already passed skillNameJSON's string boundary.
	units, _ := protocol.DecodeJavaScriptJSONString(name)
	out := utf16.Encode([]rune(prefix))
	out = append(out, units...)
	return protocol.QuoteJavaScriptUTF16(append(out, utf16.Encode([]rune(suffix))...))
}
func (s *Service) findSkill(scope *cooperative.Scope, name json.RawMessage) (SkillCommand, bool, error) {
	if !s.HasSkillBindings() {
		return SkillCommand{}, false, capability("actual shared Skill command executor")
	}
	commands, e := s.options.Skill.Commands(scope)
	if e != nil {
		return SkillCommand{}, false, e
	}
	for _, command := range commands {
		meta := command.Metadata
		var aliases []json.RawMessage
		_ = json.Unmarshal(meta.Get("aliases"), &aliases)
		if skillSameName(meta.Get("name"), name) || skillSameName(meta.Get("userFacingName"), name) {
			return command, true, nil
		}
		for _, alias := range aliases {
			if skillSameName(alias, name) {
				return command, true, nil
			}
		}
	}
	return SkillCommand{}, false, nil
}

type SkillValidationError struct {
	Message     string
	ErrorCode   int
	messageJSON json.RawMessage
}

func (e *SkillValidationError) Error() string { return e.Message }

// SkillValidationMessageJSON belongs only to Skill value-validation feedback.
// Display callers keep Error(); the existing model/receipt JSON consumers keep
// the original JavaScript units instead of decoding that display projection.
func (e *SkillValidationError) SkillValidationMessageJSON() json.RawMessage {
	return append(json.RawMessage(nil), e.messageJSON...)
}
func skillValidationError(prefix string, name json.RawMessage, suffix string, code int) *SkillValidationError {
	message := skillNameMessage(prefix, name, suffix)
	return &SkillValidationError{Message: skillDisplayName(message), ErrorCode: code, messageJSON: message}
}
func (s *Service) validateSkill(scope *cooperative.Scope, in protocol.JSONObject) error {
	name, e := skillNameJSON(in.Get("skill"))
	if e != nil {
		return e
	}
	originalUnits, e := protocol.DecodeJavaScriptJSONString(in.Get("skill"))
	if e != nil {
		return e
	}
	original := string(utf16.Decode(originalUnits))
	if skillTrim(original) == "" {
		return skillValidationError("Invalid skill format: ", in.Get("skill"), "", 1)
	}
	command, exists, e := s.findSkill(scope, name)
	if e != nil {
		return e
	}
	if !exists {
		return skillValidationError("Unknown skill: ", name, "", 2)
	}
	if string(command.Metadata.Get("disableModelInvocation")) == "true" {
		return skillValidationError("Skill ", name, " cannot be used with Skill tool due to disable-model-invocation", 4)
	}
	if command.Metadata.String("type") != "prompt" {
		return skillValidationError("Skill ", name, " is not a prompt-based skill", 5)
	}
	return nil
}

var safeSkillProperties = map[string]bool{
	"type": true, "progressMessage": true, "contentLength": true, "argNames": true, "model": true, "effort": true, "source": true, "pluginInfo": true, "disableNonInteractive": true, "skillRoot": true, "context": true, "agent": true, "getPromptForCommand": true, "frontmatterKeys": true,
	"name": true, "description": true, "hasUserSpecifiedDescription": true, "isEnabled": true, "isHidden": true, "aliases": true, "isMcp": true, "argumentHint": true, "whenToUse": true, "paths": true, "version": true, "disableModelInvocation": true, "userInvocable": true, "loadedFrom": true, "immediate": true, "userFacingName": true,
}

func skillSafe(meta protocol.JSONObject) bool {
	for _, property := range meta.OwnProperties() {
		var key string
		_ = json.Unmarshal(property.NameJSON, &key)
		if safeSkillProperties[key] || string(property.Value) == "null" {
			continue
		}
		var object protocol.JSONObject
		if json.Unmarshal(property.Value, &object) == nil && object.IsObject() && len(object.OwnProperties()) == 0 {
			continue
		}
		var array []json.RawMessage
		if json.Unmarshal(property.Value, &array) == nil && array != nil && len(array) == 0 {
			continue
		}
		return false
	}
	return true
}
func skillParsedRule(rule string) (tool, content string, hasContent bool) {
	unescaped := func(index int) bool {
		count := 0
		for i := index - 1; i >= 0 && rule[i] == '\\'; i-- {
			count++
		}
		return count%2 == 0
	}
	open, close := -1, -1
	for i := range len(rule) {
		if rule[i] == '(' && unescaped(i) {
			open = i
			break
		}
	}
	for i := len(rule) - 1; i >= 0; i-- {
		if rule[i] == ')' && unescaped(i) {
			close = i
			break
		}
	}
	if open <= 0 || close <= open || close != len(rule)-1 {
		return rule, "", false
	}
	tool = rule[:open]
	content = rule[open+1 : close]
	if content == "" || content == "*" {
		return tool, "", false
	}
	content = strings.ReplaceAll(content, `\(`, `(`)
	content = strings.ReplaceAll(content, `\)`, `)`)
	content = strings.ReplaceAll(content, `\\`, `\`)
	return tool, content, true
}
func skillRuleContent(rule string) (string, bool) {
	tool, content, present := skillParsedRule(rule)
	return content, tool == "Skill" && present
}
func skillWholeRule(c protocol.JSONObject, key, behavior string) json.RawMessage {
	var sources protocol.JSONObject
	_ = sources.UnmarshalJSON(c.Get(key))
	for _, source := range []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings", "cliArg", "command", "session"} {
		var rules []string
		_ = json.Unmarshal(sources.Get(source), &rules)
		for _, rule := range rules {
			tool, _, content := skillParsedRule(rule)
			if tool == "Skill" && !content {
				return raw(protocol.NewJSONObject(field("type", "rule"), field("rule", protocol.NewJSONObject(field("source", source), field("ruleBehavior", behavior), field("ruleValue", protocol.NewJSONObject(field("toolName", "Skill")))))))
			}
		}
	}
	return nil
}

// The syntax is shared with skillParsedRule; only Skill identities retain
// their original UTF-16 units. Other tools keep their existing string parser.
func skillRuleContentJSON(raw json.RawMessage) (json.RawMessage, bool) {
	rule, e := sdkpermissions.ParsePermissionRuleValue(raw)
	if e != nil || !skillSameName(rule.Get("toolName"), quote("Skill")) {
		return nil, false
	}
	content := rule.Get("ruleContent")
	return content, content != nil
}

func skillRules(c protocol.JSONObject, key, behavior string, name json.RawMessage) json.RawMessage {
	var sources protocol.JSONObject
	_ = sources.UnmarshalJSON(c.Get(key))
	order := []string{}
	byContent := map[string]json.RawMessage{}
	for _, source := range []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings", "cliArg", "command", "session"} {
		var rules []json.RawMessage
		_ = json.Unmarshal(sources.Get(source), &rules)
		for _, rule := range rules {
			content, present := skillRuleContentJSON(rule)
			if !present {
				continue
			}
			identity := string(content)
			if _, exists := byContent[identity]; !exists {
				order = append(order, identity)
			}
			byContent[identity] = raw(protocol.NewJSONObject(field("source", source), field("ruleBehavior", behavior), field("ruleValue", protocol.NewJSONObject(field("toolName", "Skill"), protocol.JSONField{Name: "ruleContent", Value: content}))))
		}
	}
	nameUnits, _ := protocol.DecodeJavaScriptJSONString(name)
	for _, content := range order {
		normal, _ := protocol.DecodeJavaScriptJSONString([]byte(content))
		if len(normal) > 0 && normal[0] == '/' {
			normal = normal[1:]
		}
		exact := skillSameName(protocol.QuoteJavaScriptUTF16(normal), name)
		prefix := false
		if len(normal) >= 2 && normal[len(normal)-2] == ':' && normal[len(normal)-1] == '*' {
			prefixUnits := normal[:len(normal)-2]
			prefix = len(nameUnits) >= len(prefixUnits) && skillSameName(protocol.QuoteJavaScriptUTF16(nameUnits[:len(prefixUnits)]), protocol.QuoteJavaScriptUTF16(prefixUnits))
		}
		if exact || prefix {
			return raw(protocol.NewJSONObject(field("type", "rule"), protocol.JSONField{Name: "rule", Value: byContent[content]}))
		}
	}
	return nil
}
func (s *Service) skillPermission(scope *cooperative.Scope, in *protocol.JSONObject) (queryloop.ToolPermission, error) {
	name, e := skillNameJSON(in.Get("skill"))
	if e != nil {
		return queryloop.ToolPermission{}, e
	}
	c, e := s.options.PermissionContext(scope)
	if e != nil {
		return queryloop.ToolPermission{}, e
	}
	command, exists, e := s.findSkill(scope, name)
	if e != nil {
		return queryloop.ToolPermission{}, e
	}
	decision := queryloop.ToolPermission{Behavior: "allow", Input: in, UpdatedInput: in}
	bypass := c.String("mode") == "bypassPermissions" || c.String("mode") == "plan" && string(c.Get("isBypassPermissionsModeAvailable")) == "true"
	if reason := skillWholeRule(c, "alwaysDenyRules", "deny"); reason != nil {
		decision.Behavior = "deny"
		decision.Message = quote("Permission to use Skill has been denied.")
		decision.DecisionReason = reason
		decision.UpdatedInput = nil
		return decision, nil
	}
	if reason := skillWholeRule(c, "alwaysAskRules", "ask"); reason != nil && !bypass {
		decision.Behavior = "ask"
		decision.Message = quote("Claude requested permissions to use Skill, but you haven't granted it yet.")
		decision.DecisionReason = reason
		decision.UpdatedInput = nil
		return decision, nil
	}
	if reason := skillRules(c, "alwaysDenyRules", "deny", name); reason != nil {
		decision.Behavior = "deny"
		decision.Message = quote("Skill execution blocked by permission rules")
		decision.DecisionReason = reason
		decision.UpdatedInput = nil
		return decision, nil
	}
	if bypass {
		decision.DecisionReason = raw(protocol.NewJSONObject(field("type", "mode"), field("mode", c.String("mode"))))
		return decision, nil
	}
	if reason := skillWholeRule(c, "alwaysAllowRules", "allow"); reason != nil {
		decision.DecisionReason = reason
		return decision, nil
	}
	if reason := skillRules(c, "alwaysAllowRules", "allow", name); reason != nil {
		decision.DecisionReason = reason
		return decision, nil
	}
	if exists && command.Metadata.String("type") == "prompt" && skillSafe(command.Metadata) {
		return decision, nil
	}
	decision.Behavior = "ask"
	decision.Message = skillNameMessage("Execute skill: ", name, "")
	decision.Suggestions = raw([]protocol.JSONObject{protocol.NewJSONObject(field("type", "addRules"), field("rules", []protocol.JSONObject{protocol.NewJSONObject(field("toolName", "Skill"), protocol.JSONField{Name: "ruleContent", Value: name})}), field("behavior", "allow"), field("destination", "localSettings")), protocol.NewJSONObject(field("type", "addRules"), field("rules", []protocol.JSONObject{protocol.NewJSONObject(field("toolName", "Skill"), protocol.JSONField{Name: "ruleContent", Value: skillNameMessage("", name, ":*")})}), field("behavior", "allow"), field("destination", "localSettings"))})
	if exists {
		decision.Metadata = raw(protocol.NewJSONObject(field("command", command.Metadata)))
	}
	return decision, nil
}
func (s *Service) callSkill(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
	scope := scopeFor(call.Opaque)
	if e := s.check(scope); e != nil {
		return queryloop.ToolCallResult{}, e
	}
	name, e := skillNameJSON(in.Get("skill"))
	if e != nil {
		return queryloop.ToolCallResult{}, e
	}
	command, exists, e := s.findSkill(scope, name)
	if e != nil {
		return queryloop.ToolCallResult{}, e
	}
	if !exists {
		return queryloop.ToolCallResult{}, errors.New("Command processing failed")
	}
	var args *string
	if in.Get("args") != nil {
		value, e := text(in.Get("args"))
		if e != nil {
			return queryloop.ToolCallResult{}, e
		}
		args = &value
	}
	command.InvocationNameJSON = append(json.RawMessage(nil), name...)
	return s.options.Skill.Call(ctx, scope, skillDisplayName(name), command, args, call)
}
func mapSkillResult(data, id json.RawMessage) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	if e := result.UnmarshalJSON(data); e != nil {
		return protocol.JSONObject{}, e
	}
	content, e := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("Launching skill: "), result.Get("commandName")}, "")
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if result.String("status") == "forked" {
		var e error
		content, e = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("Skill \""), result.Get("commandName"), quote("\" completed (forked execution).\n\nResult:\n"), result.Get("result")}, "")
		if e != nil {
			return protocol.JSONObject{}, e
		}
	}
	return protocol.NewJSONObject(field("type", "tool_result"), protocol.JSONField{Name: "tool_use_id", Value: id}, protocol.JSONField{Name: "content", Value: content}), nil
}
