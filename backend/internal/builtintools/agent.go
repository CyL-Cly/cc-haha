package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

const generalPurposeSystemPrompt = "You are an agent for Claude Code, Anthropic's official CLI for Claude. Given the user's message, you should use the tools available to complete the task. Complete the task fully—don't gold-plate, but don't leave it half-done. When you complete the task, respond with a concise report covering what was done and any key findings — the caller will relay this to the user, so it only needs the essentials.\n\nYour strengths:\n- Searching for code, configurations, and patterns across large codebases\n- Analyzing multiple files to understand system architecture\n- Investigating complex questions that require exploring many files\n- Performing multi-step research tasks\n\nGuidelines:\n- For file searches: search broadly when you don't know where something lives. Use Read when you know the specific file path.\n- For analysis: Start broad and narrow down. Use multiple search strategies if the first doesn't yield results.\n- Be thorough: Check multiple locations, consider different naming conventions, look for related files.\n- NEVER create files unless they're absolutely necessary for achieving your goal. ALWAYS prefer editing an existing file to creating a new one.\n- NEVER proactively create documentation files (*.md) or README files. Only create documentation files if explicitly requested."

const agentNotes = "Notes:\n- Agent threads always have their cwd reset between bash calls, as a result please only use absolute file paths.\n- In your final response, share file paths (always absolute, never relative) that are relevant to the task. Include code snippets only when the exact text is load-bearing (e.g., a bug you found, a function signature the caller asked for) — do not recap code you merely read.\n- For clear communication with the user the assistant MUST avoid using emojis.\n- Do not use a colon before tool calls. Text like \"Let me read the file:\" followed by a read tool call should just be \"Let me read the file.\" with a period."

func (s *Service) startAgent(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	scope := scopeFor(call.Opaque)
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if matched, future, err := s.teamAgent(ctx, scope, in, call); matched {
		return future, err
	}
	if s.options.RunAgent == nil {
		return nil, capability("Agent shared child Controller/Harness runner")
	}
	background := boolean(in, "run_in_background", false) && !taskEnvironmentTruthy(s.options.Environment["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"])
	if background && s.options.RunBackgroundAgent == nil {
		return nil, capability("Agent background task registry/notifications and parent-independent cancellation")
	}
	if in.String("team_name") != "" || in.String("mode") != "" {
		return nil, capability("Agent teammate lifecycle and explicit teammate mode")
	}
	if in.String("name") != "" && (s.options.BackgroundTasks == nil || s.options.BackgroundTasks.RegisterName == nil) {
		return nil, capability("Agent name registry/SendMessage continuation")
	}
	if in.String("cwd") != "" {
		return nil, capability("Agent feature-gated cwd override")
	}
	agentType := in.String("subagent_type")
	if agentType == "" {
		agentType = "general-purpose"
	}
	permission, e := s.options.PermissionContext(scope)
	if e != nil {
		return nil, e
	}
	if source, denied := agentDeniedSource(permission, agentType); denied {
		return nil, fmt.Errorf("Agent type '%s' has been denied by permission rule 'Agent(%s)' from %s.", agentType, agentType, source)
	}
	if s.options.AgentDefinitions == nil && agentType != "general-purpose" {
		return nil, capability("Agent definition/loading/system prompt for " + agentType)
	}
	selected := agentcatalog.Definition{Name: "general-purpose", Source: "built-in", SystemPrompt: generalPurposeSystemPrompt, Fields: protocol.NewJSONObject(field("tools", []string{"*"}))}
	if s.options.AgentDefinitions != nil {
		definitions, err := s.options.AgentDefinitions(scope)
		if err != nil {
			return nil, err
		}
		available := []string{}
		found := false
		for _, definition := range definitions {
			if _, denied := agentDeniedSource(permission, definition.Name); denied {
				continue
			}
			available = append(available, definition.Name)
			if definition.Name == agentType && !found {
				selected, found = definition, true
			}
		}
		if !found {
			return nil, fmt.Errorf("Agent type '%s' not found. Available agents: %s", agentType, strings.Join(available, ", "))
		}
	}
	background = background || string(selected.Fields.Get("background")) == "true" && !taskEnvironmentTruthy(s.options.Environment["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"])
	if background && s.options.RunBackgroundAgent == nil {
		return nil, capability("Agent configured background task registry/notifications")
	}
	var nativePresetRunner func(context.Context, *cooperative.Scope, AgentRequest) (*ownerasync.OwnerFuture[AgentRunResult], error)
	if permissionFeedbackValue(selected.Fields.Get("hooks")) {
		var bound bool
		nativePresetRunner, bound = s.nativeAgentMCPRunner(scope, background)
		if !bound {
			return nil, capability("Agent configured hooks lifecycle")
		}
	}
	if permissionFeedbackValue(selected.Fields.Get("mcpServers")) {
		var bound bool
		nativePresetRunner, bound = s.nativeAgentMCPRunner(scope, background)
		if !bound {
			return nil, capability("Agent configured mcpServers lifecycle")
		}
	}
	isolation := selected.Fields.String("isolation")
	if in.Get("isolation") != nil {
		isolation = in.String("isolation")
	}
	if isolation != "" && (isolation != "worktree" || !s.HasWorktreeBindings()) {
		return nil, capability("Agent actual worktree isolation/lifecycle")
	}
	var skills []string
	_ = json.Unmarshal(selected.Fields.Get("skills"), &skills)
	var nativeSkillRunner func(context.Context, *cooperative.Scope, AgentRequest) (*ownerasync.OwnerFuture[AgentRunResult], error)
	if len(skills) > 0 {
		var bound bool
		nativeSkillRunner, bound = s.nativeAgentSkillRunner(scope, background)
		if !bound {
			return nil, capability("Agent configured skill preload lifecycle")
		}
	}
	_, registered, e := s.Catalog(scope)
	if e != nil {
		return nil, e
	}
	availableTools := []string{}
	for _, tool := range registered {
		availableTools = append(availableTools, tool.Name)
	}
	if s.options.AgentAdditionalTools != nil {
		additional, err := s.options.AgentAdditionalTools(scope)
		if err != nil {
			return nil, err
		}
		for _, tool := range additional {
			availableTools = append(availableTools, tool.Name)
		}
	}
	resolved := agentcatalog.ResolveTools(selected, availableTools, false, false)
	prompt := selected.SystemPrompt
	promptJSON := quote(prompt)
	if s.options.AgentPromptJSON != nil {
		promptJSON, e = s.options.AgentPromptJSON(scope, selected)
		if e != nil {
			return nil, e
		}
		if e = json.Unmarshal(promptJSON, &prompt); e != nil {
			return nil, e
		}
	} else if s.options.AgentPrompt != nil {
		prompt, e = s.options.AgentPrompt(scope, selected)
		if e != nil {
			return nil, e
		}
		promptJSON = quote(prompt)
	}

	childPermission := permission.Clone()
	override := selected.Fields.String("permissionMode")
	if (selected.Source == "projectSettings" || selected.Source == "localSettings") && override == "bypassPermissions" {
		override = ""
	}
	if override != "" && permission.String("mode") != "bypassPermissions" && permission.String("mode") != "acceptEdits" && permission.String("mode") != "auto" {
		childPermission.Set("mode", quote(override))
	}
	var maxTurns *float64
	if raw := selected.Fields.Get("maxTurns"); raw != nil {
		var value float64
		if json.Unmarshal(raw, &value) == nil {
			maxTurns = &value
		}
	}
	request := AgentRequest{AgentToolInvocation: true, SelectedAgent: selected.Fields.Clone(), Prompt: in.String("prompt"), Description: in.String("description"), AgentType: agentType, Model: in.String("model"), DefinitionModel: selected.Model, Source: selected.Source, Effort: append(json.RawMessage(nil), selected.Effort...), MaxTurns: maxTurns, OmitClaudeMd: string(selected.Fields.Get("omitClaudeMd")) == "true", SystemPrompt: prompt + "\n\n" + agentNotes, SystemPromptSections: []json.RawMessage{append(json.RawMessage(nil), promptJSON...), quote(agentNotes)}, PermissionContext: childPermission, Tools: resolved.ResolvedTools, ToolUseID: append(json.RawMessage(nil), call.ToolUseID...)}
	request.ParentPermissionMode, request.PermissionModeOverride = permission.String("mode"), override
	request.Name = in.String("name")
	request.Isolation = isolation
	if original, ok := call.Opaque.(*builtinwrite.CallContext); ok && original != nil {
		request.ParentModel = original.MainLoopModel
		if original.ToolSearchCatalogue != nil {
			for _, tool := range original.ToolSearchCatalogue.Tools {
				var toolName string
				_ = json.Unmarshal(tool.Name, &toolName)
				request.ParentTools = append(request.ParentTools, toolName)
				if string(tool.Name) == `"Read"` || string(tool.Name) == `"Bash"` {
					request.CanReadOutputFile = true
				}
			}
		}
	}
	runner := s.options.RunAgent
	if background {
		runner = s.options.RunBackgroundAgent
	}
	if nativeSkillRunner != nil {
		runner = nativeSkillRunner
	}
	if nativePresetRunner != nil {
		runner = nativePresetRunner
	}
	future, e := runner(ctx, scope, request)
	if e != nil {
		return nil, e
	}
	if future == nil {
		return nil, capability("Agent actual child completion future")
	}
	return transform(s, scope, future, func(_ *cooperative.Scope, run AgentRunResult) (queryloop.ToolCallResult, error) {
		data, e := finalizeAgent(request, run)
		return queryloop.ToolCallResult{Data: data}, e
	})
}

func permissionFeedbackValue(raw json.RawMessage) bool {
	return len(raw) != 0 && string(raw) != "null" && string(raw) != "false" && string(raw) != `""` && string(raw) != "[]" && string(raw) != "{}"
}

func agentDeniedSource(permission protocol.JSONObject, agentType string) (string, bool) {
	var sources protocol.JSONObject
	_ = sources.UnmarshalJSON(permission.Get("alwaysDenyRules"))
	for _, property := range sources.OwnProperties() {
		var rules []string
		_ = json.Unmarshal(property.Value, &rules)
		for _, rule := range rules {
			name, content := agentcatalog.ParseToolRule(rule)
			if name == "Agent" && content == agentType {
				var source string
				_ = json.Unmarshal(property.NameJSON, &source)
				return source, true
			}
		}
	}
	return "", false
}

func finalizeAgent(request AgentRequest, run AgentRunResult) (json.RawMessage, error) {
	if run.AgentID == "" || math.IsNaN(run.DurationMillis) || math.IsInf(run.DurationMillis, 0) || run.DurationMillis < 0 {
		return nil, capability("Agent actual lifecycle identity/duration")
	}
	if run.Async {
		if run.OutputFile == "" {
			return nil, capability("Agent actual asynchronous output path")
		}
		data := protocol.NewJSONObject(field("isAsync", true), field("status", "async_launched"), field("agentId", run.AgentID), field("description", request.Description), field("prompt", request.Prompt), field("outputFile", run.OutputFile), field("canReadOutputFile", request.CanReadOutputFile))
		if run.WorktreeIsolationSkipped != "" {
			data.Set("worktreeIsolationSkipped", quote(run.WorktreeIsolationSkipped))
		}
		return data.MarshalJSON()
	}
	var last protocol.JSONObject
	content := []protocol.JSONObject{}
	toolUses := 0
	for _, message := range run.Messages {
		if message.Type() != "assistant" {
			continue
		}
		var api protocol.JSONObject
		originalMessage := message.Record.Get("message")
		if message.Assistant != nil {
			var e error
			originalMessage, e = message.Assistant.MessageJSON()
			if e != nil {
				return nil, e
			}
		}
		if e := api.UnmarshalJSON(originalMessage); e != nil {
			return nil, e
		}
		last = api
		var blocks []protocol.JSONObject
		if e := json.Unmarshal(api.Get("content"), &blocks); e != nil {
			return nil, e
		}
		textBlocks := []protocol.JSONObject{}
		for _, block := range blocks {
			switch block.String("type") {
			case "tool_use":
				toolUses++
			case "text":
				textBlocks = append(textBlocks, block.Clone())
			}
		}
		if len(textBlocks) > 0 {
			content = textBlocks
		}
	}
	if !last.IsObject() {
		return nil, errors.New("No assistant messages found")
	}
	var usage protocol.JSONObject
	if e := usage.UnmarshalJSON(last.Get("usage")); e != nil || !usage.IsObject() {
		return nil, capability("Agent actual last assistant usage")
	}
	tokens := 0.0
	for _, key := range []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens"} {
		var value float64
		if json.Unmarshal(usage.Get(key), &value) == nil && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 {
			tokens += value
		}
	}
	data := protocol.NewJSONObject(field("status", "completed"), field("prompt", request.Prompt), field("agentId", run.AgentID), field("agentType", request.AgentType), field("content", content), field("totalDurationMs", run.DurationMillis), field("totalTokens", tokens), field("totalToolUseCount", toolUses), protocol.JSONField{Name: "usage", Value: last.Get("usage")})
	if run.WorktreePath != "" {
		data.Set("worktreePath", quote(run.WorktreePath))
	}
	if run.WorktreeBranch != "" {
		data.Set("worktreeBranch", quote(run.WorktreeBranch))
	}
	if run.WorktreeIsolationSkipped != "" {
		data.Set("worktreeIsolationSkipped", quote(run.WorktreeIsolationSkipped))
	}
	return data.MarshalJSON()
}

// FinalizeAgentRun is the shared original Agent result producer used when a
// detached native harness completes. It accepts original history, not a
// rendered tool result or a synthetic usage snapshot.
func FinalizeAgentRun(request AgentRequest, run AgentRunResult) (json.RawMessage, error) {
	return finalizeAgent(request, run)
}

func mapAgent(result protocol.JSONObject, id json.RawMessage) (protocol.JSONObject, error) {
	if result.String("status") == "teammate_staged" {
		// The original producer stages the durable draft, but its original
		// mapper has no staged branch. Preserve the same model-visible error.
		return protocol.JSONObject{}, errors.New("Unexpected agent tool result status: teammate_staged")
	}
	if result.String("status") == "teammate_spawned" {
		text := "Spawned successfully.\nagent_id: " + result.String("teammate_id") + "\nname: " + result.String("name") + "\nteam_name: " + result.String("team_name") + "\nThe agent is now running and will receive instructions via mailbox."
		return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), field("content", []protocol.JSONObject{protocol.NewJSONObject(field("type", "text"), field("text", text))})), nil
	}
	if result.String("status") == "async_launched" {
		agentID := result.String("agentId")
		prefix := "Async agent launched successfully.\nagentId: " + agentID + " (internal ID - do not mention to user. Use SendMessage with to: '" + agentID + "' to continue this agent.)\nThe agent is working in the background. You will be notified automatically when it completes."
		stop := "Do not stop this agent just because you have enough partial output. Stop it only if the user asks to cancel it, or if it is clearly runaway, harmful, duplicative, or no longer useful."
		instructions := "Briefly tell the user what you launched and end your response. Do not generate any other text — agent results will arrive in a subsequent message.\n" + stop
		if boolean(result, "canReadOutputFile", false) {
			instructions = "Do not duplicate this agent's work — avoid working with the same files or topics it is using. Work on non-overlapping tasks, or briefly tell the user what you launched and end your response.\n" + stop + "\noutput_file: " + result.String("outputFile") + "\nIf asked, you can check progress before completion by using Read or Bash tail on the output file."
		}
		isolation := ""
		if skipped := result.String("worktreeIsolationSkipped"); skipped != "" {
			isolation = "\nworktree isolation was skipped (" + skipped + "); the agent runs in the workspace directory."
		}
		return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), field("content", []protocol.JSONObject{protocol.NewJSONObject(field("type", "text"), field("text", prefix+isolation+"\n"+instructions))})), nil
	}
	if result.String("status") != "completed" {
		return protocol.JSONObject{}, capability("Agent asynchronous/teammate result mapping")
	}
	var content []protocol.JSONObject
	if e := json.Unmarshal(result.Get("content"), &content); e != nil {
		return protocol.JSONObject{}, e
	}
	if len(content) == 0 {
		content = []protocol.JSONObject{protocol.NewJSONObject(field("type", "text"), field("text", "(Subagent completed but returned no output.)"))}
	}
	agentID := result.String("agentId")
	worktreeText := ""
	if path := result.String("worktreePath"); path != "" {
		branch := "undefined"
		if result.Get("worktreeBranch") != nil {
			branch = result.String("worktreeBranch")
		}
		worktreeText = "\nworktreePath: " + path + "\nworktreeBranch: " + branch
	} else if skipped := result.String("worktreeIsolationSkipped"); skipped != "" {
		worktreeText = "\nworktree isolation was skipped (" + skipped + "); the agent ran in the workspace directory."
	}
	if worktreeText == "" && (result.String("agentType") == "Explore" || result.String("agentType") == "Plan") {
		return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), field("content", content)), nil
	}
	trailer := fmt.Sprintf("agentId: %s (use SendMessage with to: '%s' to continue this agent)%s\n<usage>total_tokens: %s\ntool_uses: %s\nduration_ms: %s</usage>", agentID, agentID, worktreeText, numberText(result.Get("totalTokens")), numberText(result.Get("totalToolUseCount")), numberText(result.Get("totalDurationMs")))
	content = append(content, protocol.NewJSONObject(field("type", "text"), field("text", trailer)))
	return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), field("content", content)), nil
}
func numberText(value json.RawMessage) string {
	return strings.TrimSpace(string(protocol.StringifyValidatedJavaScriptJSON(value)))
}
