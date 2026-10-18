package builtintools

import (
	"context"
	"encoding/json"
	"math"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

func (s *Service) Catalog(scope *cooperative.Scope) ([]toolschema.Definition, []queryloop.RegisteredTool, error) {
	if e := s.check(scope); e != nil {
		return nil, nil, e
	}
	if s.options.AgentDefinitions != nil {
		definitions, err := s.options.AgentDefinitions(scope)
		if err != nil {
			return nil, nil, err
		}
		s.agentSnapshot = definitions
	}
	defs := []toolschema.Definition{}
	tools := []queryloop.RegisteredTool{}
	permissionContext, e := s.options.PermissionContext(scope)
	if e != nil {
		return nil, nil, e
	}
	writeDefs, writeTools, e := s.options.Write.Catalog(scope)
	if e != nil {
		return nil, nil, e
	}
	images := false
	if s.HasImageBindings() {
		config, err := s.options.Images.Config(scope)
		if err != nil {
			return nil, nil, err
		}
		if config != nil {
			images = true
		}
	}
	names := nativeBaseToolNames(images)
	names, e = s.shellCatalogNames(names)
	if e != nil {
		return nil, nil, e
	}
	for _, name := range names {
		if teamTool(name) {
			if !s.HasTeamBindings() {
				continue
			}
			enabled, e := s.options.Team.Enabled(scope)
			if e != nil {
				return nil, nil, e
			}
			if !enabled || name == "TeamPlan" && (!s.options.Team.ReviewRequired || s.options.Team.IsTeammate) {
				continue
			}
		}
		if name == "Workflow" {
			if !s.HasWorkflowBindings() {
				continue
			}
			enabled, e := s.options.Workflow.Enabled(scope)
			if e != nil {
				return nil, nil, e
			}
			if !enabled {
				continue
			}
		}
		if name == "SendMessage" {
			enabled, e := s.sendMessageEnabled(scope)
			if e != nil {
				return nil, nil, e
			}
			if !enabled || s.options.ResumeAgent == nil || s.options.BackgroundTasks == nil || s.options.BackgroundTasks.ResolveName == nil {
				continue
			}
		}
		if sessionTool(name) && !s.HasSessionCollaborationBindings() {
			continue
		}
		if name == "ToolSearch" {
			if !s.HasToolSearchBindings() {
				continue
			}
			enabled, e := s.options.ToolSearch.Enabled(scope)
			if e != nil {
				return nil, nil, e
			}
			if !enabled {
				continue
			}
		}
		if backgroundTaskTool(name) && !s.HasBackgroundTaskBindings() {
			continue
		}
		if mcpResourceTool(name) {
			if skillSimple(s.options.Environment["CLAUDE_CODE_SIMPLE"]) {
				continue
			}
			if !s.HasMCPResourceBindings() {
				continue
			}
			enabled, e := s.options.MCPResources.Available(scope)
			if e != nil {
				return nil, nil, e
			}
			if !enabled {
				continue
			}
		}
		if skillSimple(s.options.Environment["CLAUDE_CODE_SIMPLE"]) && name != "Bash" && name != "PowerShell" && name != "Read" && name != "Edit" {
			continue
		}
		if name == "Skill" && !s.HasSkillBindings() {
			continue
		}
		if worktreeTool(name) && !s.HasWorktreeBindings() {
			continue
		}
		if planTool(name) && !s.HasPlanBindings() {
			continue
		}
		if name == "WebSearch" {
			enabled, e := s.webSearchEnabled(scope)
			if e != nil {
				return nil, nil, e
			}
			if !enabled {
				continue
			}
		}
		if name == "WebFetch" && !s.HasWebFetchBindings() {
			continue
		}
		if name == "Agent" && s.options.RunAgent == nil {
			continue
		}
		if name == "TodoWrite" && !s.todoEnabled() {
			continue
		}
		if taskTool(name) && !s.taskEnabled() {
			continue
		}
		if wholeDenied(permissionContext, name) {
			continue
		}
		if name == "Write" {
			defs = append(defs, writeDefs...)
			tools = append(tools, writeTools...)
			continue
		}
		definition, e := toolschema.NativeBuiltinDefinition(name)
		if e != nil {
			return nil, nil, e
		}
		if name == "PowerShell" {
			definition = toolschema.PowerShellDefinition(s.options.Environment)
		}
		if name == "Agent" && taskEnvironmentTruthy(s.options.Environment["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"]) {
			// Source constructs this local Zod schema with the startup flag.
			// Keep the parser, API schema and compaction budget on that same
			// definition; API-only filtering would still charge the hidden field.
			input := *definition.InputSchema
			input.Fields = make([]toolschema.Field, 0, len(definition.InputSchema.Fields)-1)
			for _, field := range definition.InputSchema.Fields {
				if field.Name != "run_in_background" {
					input.Fields = append(input.Fields, field)
				}
			}
			definition.InputSchema = &input
		}
		tool := queryloop.RegisteredTool{Name: name, AlwaysLoad: definition.AlwaysLoad, ShouldDefer: definition.ShouldDefer, SearchHint: definition.SearchHint, PersistenceThreshold: 100000, ParseInput: func(in protocol.JSONObject) (protocol.JSONObject, error) {
			return toolschema.ParseInput(definition, in)
		}, IsConcurrencySafe: func(protocol.JSONObject) bool {
			return imageTool(name) || name == "Agent" || name == "Read" || name == "Glob" || name == "Grep" || name == "AskUserQuestion" || isWebTool(name) || name == "TaskCreate" || name == "TaskUpdate" || planTool(name) || mcpResourceTool(name)
		}, ValidateInput: func(ctx context.Context, in protocol.JSONObject, v any) error {
			return s.Validate(ctx, name, in, scopeFor(v))
		}, CallJSON: func(ctx context.Context, in protocol.JSONObject, c queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
			var parent json.RawMessage
			if c.Invocation.Assistant != nil {
				parent = c.Invocation.Assistant.Record.Get("uuid")
			}
			var data json.RawMessage
			var e error
			if name == "Edit" {
				data, e = s.editWithParent(ctx, in, scopeFor(c.Opaque), c.UserModified, parent)
			} else if name == "NotebookEdit" {
				data, e = s.notebookEditWithParent(ctx, in, scopeFor(c.Opaque), parent)
			} else {
				data, e = s.Call(ctx, name, in, scopeFor(c.Opaque), c.UserModified)
			}
			return queryloop.ToolCallResult{Data: data}, e
		}, MapResultJSON: func(data, id json.RawMessage) (protocol.JSONObject, error) { return MapResult(name, data, id) }}
		if taskTool(name) {
			tool.MapResultJSON = func(data, id json.RawMessage) (protocol.JSONObject, error) {
				var result protocol.JSONObject
				if e := result.UnmarshalJSON(data); e != nil {
					return protocol.JSONObject{}, e
				}
				return mapTask(name, result, id, s.options.Team != nil && s.options.Team.IsTeammate)
			}
		}
		if name == "Read" {
			tool.PersistenceThreshold = math.Inf(1)
		}
		if name == "Grep" {
			tool.PersistenceThreshold = 20_000
		}
		if name == "Bash" {
			tool.PersistenceThreshold = 30_000
			tool.CallJSON = s.callBash
			tool.MapResultJSON = s.mapBashResult
		}
		if name == "PowerShell" {
			tool.PersistenceThreshold = 30_000
			tool.CallJSON = s.callPowerShell
			tool.MapResultJSON = s.mapPowerShellResult
			tool.ValidateInput = func(_ context.Context, _ protocol.JSONObject, opaque any) error {
				return s.validatePowerShell(scopeFor(opaque))
			}
		}
		if imageTool(name) {
			tool.ValidateInput = nil
			tool.StartCallJSON = s.startImage
			tool.CallJSON = func(ctx context.Context, input protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
				future, err := s.startImage(ctx, input, call)
				if err != nil {
					return queryloop.ToolCallResult{}, err
				}
				return future.Await(scopeFor(call.Opaque))
			}
			tool.MapResultJSON = mapImageResult
		}
		if backgroundTaskTool(name) {
			tool.IsConcurrencySafe = func(protocol.JSONObject) bool { return true }
			tool.StartCallJSON = func(ctx context.Context, in protocol.JSONObject, c queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
				return s.startBackgroundTask(ctx, name, in, c)
			}
			tool.CallJSON = func(ctx context.Context, in protocol.JSONObject, c queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
				data, e := s.callBackgroundTask(ctx, scopeFor(c.Opaque), name, in, c.OnProgress)
				return queryloop.ToolCallResult{Data: data}, e
			}
			tool.MapResultJSON = func(data, id json.RawMessage) (protocol.JSONObject, error) {
				return s.mapBackgroundTask(name, data, id)
			}
			if name == "TaskOutput" {
				tool.Aliases = []string{"AgentOutputTool", "BashOutputTool"}
			} else {
				tool.Aliases = []string{"KillShell"}
			}
		}
		if name == "WebSearch" {
			tool.StartCallJSON = s.startWebSearch
			tool.CallJSON = func(ctx context.Context, in protocol.JSONObject, c queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
				future, e := s.startWebSearch(ctx, in, c)
				if e != nil {
					return queryloop.ToolCallResult{}, e
				}
				return future.Await(scopeFor(c.Opaque))
			}
		}
		if name == "WebFetch" {
			tool.StartCallJSON = s.startWebFetch
			tool.CallJSON = func(ctx context.Context, in protocol.JSONObject, c queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
				future, e := s.startWebFetch(ctx, in, c)
				if e != nil {
					return queryloop.ToolCallResult{}, e
				}
				return future.Await(scopeFor(c.Opaque))
			}
		}
		if worktreeTool(name) {
			tool.StartCallJSON = func(ctx context.Context, in protocol.JSONObject, c queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
				return s.startWorktree(ctx, name, in, c)
			}
			tool.CallJSON = func(ctx context.Context, in protocol.JSONObject, c queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
				future, e := s.startWorktree(ctx, name, in, c)
				if e != nil {
					return queryloop.ToolCallResult{}, e
				}
				return future.Await(scopeFor(c.Opaque))
			}
			tool.MapResultJSON = mapWorktreeResult
		}
		if name == "Workflow" {
			// Source has no optional validateInput handler here. Structured
			// schema parsing and the actual launcher perform its validation.
			tool.ValidateInput = nil
			tool.CallJSON = s.callWorkflow
			tool.MapResultJSON = mapWorkflowResult
		}
		if teamTool(name) {
			tool.IsConcurrencySafe = func(protocol.JSONObject) bool { return false }
			tool.StartCallJSON = func(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
				return s.startTeam(ctx, name, in, call)
			}
			tool.CallJSON = func(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
				future, e := s.startTeam(ctx, name, in, call)
				if e != nil {
					return queryloop.ToolCallResult{}, e
				}
				return future.Await(scopeFor(call.Opaque))
			}
			tool.MapResultJSON = mapTeamResult
			if name != "TeamCreate" {
				tool.ValidateInput = nil
			}
		}
		if name == "Skill" {
			tool.CallJSON = s.callSkill
			tool.MapResultJSON = mapSkillResult
		}
		if mcpResourceTool(name) {
			tool.StartCallJSON = func(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
				return s.startMCPResource(ctx, name, in, call)
			}
		}
		if name == "SendMessage" {
			tool.CallJSON = s.callSendMessage
			tool.Backfill = sendMessageBackfill
			tool.IsConcurrencySafe = func(protocol.JSONObject) bool { return false }
		}
		if name == "Agent" {
			// Actual catalogue search consumes the same native tool metadata.
			tool.Aliases = []string{"Task"}
			tool.StartCallJSON = s.startAgent
			tool.CallJSON = func(ctx context.Context, in protocol.JSONObject, c queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
				future, e := s.startAgent(ctx, in, c)
				if e != nil {
					return queryloop.ToolCallResult{}, e
				}
				return future.Await(scopeFor(c.Opaque))
			}
		}
		if name == "ToolSearch" {
			tool.IsConcurrencySafe = func(protocol.JSONObject) bool { return true }
			tool.StartCallJSON = s.startToolSearch
			tool.CallJSON = func(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
				future, e := s.startToolSearch(ctx, in, call)
				if e != nil {
					return queryloop.ToolCallResult{}, e
				}
				return future.Await(scopeFor(call.Opaque))
			}
			tool.MapResultJSON = mapToolSearch
		}
		if name == "AskUserQuestion" {
			tool.StartCallJSON = s.startQuestion
		}
		if sessionTool(name) {
			tool.IsConcurrencySafe = func(protocol.JSONObject) bool { return name != "CreateSession" && name != "SendSessionMessage" }
			tool.StartCallJSON = func(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
				return s.startSessionCollaboration(ctx, name, in, call)
			}
			tool.CallJSON = func(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
				future, e := s.startSessionCollaboration(ctx, name, in, call)
				if e != nil {
					return queryloop.ToolCallResult{}, e
				}
				return future.Await(scopeFor(call.Opaque))
			}
			tool.MapResultJSON = mapSessionCollaboration
		}
		if planTool(name) {
			tool.StartCallJSON = func(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
				return s.startPlanMode(ctx, name, in, call)
			}
			if name == "ExitPlanMode" {
				tool.NormalizeInputJSON = func(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (protocol.JSONObject, error) {
					current := scopeFor(call.Opaque)
					if e := s.check(current); e != nil {
						return protocol.JSONObject{}, e
					}
					return s.options.PlanMode.Plans.NormalizeExitInput(current, in, s.options.PlanMode.AgentID)
				}
			}
			tool.MapResultJSON = func(data, id json.RawMessage) (protocol.JSONObject, error) {
				return s.mapPlanResult(nil, name, data, id)
			}
		}
		if taskTool(name) {
			tool.CallJSON = func(ctx context.Context, in protocol.JSONObject, c queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
				future, e := s.startTask(ctx, name, in, c)
				if e != nil {
					return queryloop.ToolCallResult{}, e
				}
				return future.Await(scopeFor(c.Opaque))
			}
			if name == "TaskCreate" || name == "TaskUpdate" {
				tool.StartCallJSON = func(ctx context.Context, in protocol.JSONObject, c queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
					return s.startTask(ctx, name, in, c)
				}
			}
		}
		if name == "Read" || name == "Glob" || name == "Grep" {
			tool.StartCallJSON = func(ctx context.Context, in protocol.JSONObject, c queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
				scope := scopeFor(c.Opaque)
				if e := s.check(scope); e != nil {
					return nil, e
				}
				if e := context.Cause(ctx); e != nil {
					return nil, e
				}
				var future *ownerasync.OwnerFuture[json.RawMessage]
				var e error
				switch name {
				case "Read":
					future, e = s.startRead(readAnalyticsContext(ctx, c), in, scope)
				case "Glob":
					future, e = s.startGlob(ctx, in, scope)
				case "Grep":
					future, e = s.startGrep(ctx, in, scope)
				}
				if e != nil {
					return nil, e
				}
				return transform(s, scope, future, func(next *cooperative.Scope, data json.RawMessage) (queryloop.ToolCallResult, error) {
					if name == "Read" {
						return s.readResult(next, data)
					}
					return queryloop.ToolCallResult{Data: data}, nil
				})
			}
		}
		if name == "Read" {
			tool.CallJSON = func(ctx context.Context, in protocol.JSONObject, c queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
				scope := scopeFor(c.Opaque)
				data, err := s.read(readAnalyticsContext(ctx, c), in, scope)
				if err != nil {
					return queryloop.ToolCallResult{}, err
				}
				return s.readResult(scope, data)
			}
		}
		if name == "Read" || name == "Edit" {
			tool.Backfill = func(in *protocol.JSONObject) error {
				p, e := s.expand(in.Get("file_path"))
				if e == nil {
					in.Set("file_path", quote(p))
				}
				return e
			}
		}
		defs = append(defs, definition)
		tools = append(tools, tool)
	}
	if s.lspAvailable() && !wholeDenied(permissionContext, "LSP") {
		definition, tool := s.lspCatalog()
		defs = append(defs, definition)
		tools = append(tools, tool)
	}
	return defs, tools, nil
}

func (s *Service) SchemaHost(model, baseURL string) *toolschema.Host {
	view := map[string]string{}
	for _, key := range []string{"CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS", "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS", "BASH_DEFAULT_TIMEOUT_MS", "BASH_MAX_TIMEOUT_MS"} {
		if value, ok := s.options.Environment[key]; ok {
			view[key] = value
		}
	}
	if value, ok := s.options.Environment["BASH_MAX_OUTPUT_LENGTH"]; ok {
		view["BASH_MAX_OUTPUT_LENGTH"] = value
	}
	host := &toolschema.Host{MainLoopModel: model, BaseURL: baseURL, BashEnvironment: view, QuestionPreviewFormat: s.options.QuestionPreviewFormat, PlanUserType: s.options.Environment["USER_TYPE"]}
	if s.options.Shells != nil {
		if shells, ready := s.options.Shells.Snapshot(); ready {
			host.PowerShellEdition = windowshell.Edition(shells.PowerShell)
		}
	}
	if s.HasTeamBindings() {
		host.TeamReviewRequired = s.options.Team.ReviewRequired
	}
	if s.HasWebSearchBindings() {
		host.WebSearchMonthYear = s.options.WebSearch.MonthYear()
	}
	if s.HasPlanBindings() {
		host.PlanInterviewPhase, _ = s.options.PlanMode.InterviewPhase()
	}
	if s.options.RunAgent != nil {
		host.AgentDefinitions = []toolschema.AgentPromptDefinition{{Type: "general-purpose", WhenToUse: "General-purpose agent for researching complex questions, searching for code, and executing multi-step tasks. When you are searching for a keyword or file and are not confident that you will find the right match in the first few tries use this agent to perform the search for you.", Tools: []string{"*"}}}
		if s.options.AgentDefinitions != nil {
			host.AgentDefinitions = []toolschema.AgentPromptDefinition{}
			for _, definition := range s.agentSnapshot {
				var tools, denied []string
				_ = json.Unmarshal(definition.Fields.Get("tools"), &tools)
				_ = json.Unmarshal(definition.Fields.Get("disallowedTools"), &denied)
				host.AgentDefinitions = append(host.AgentDefinitions, toolschema.AgentPromptDefinition{Type: definition.Name, WhenToUse: definition.Description, Tools: tools, DisallowedTools: denied})
			}
		}
	}
	return host
}

func (s *Service) SchemaHostForScope(scope *cooperative.Scope, model, baseURL string) (*toolschema.Host, error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if s.options.AgentDefinitions != nil {
		definitions, err := s.options.AgentDefinitions(scope)
		if err != nil {
			return nil, err
		}
		s.agentSnapshot = definitions
	}
	host := s.SchemaHost(model, baseURL)
	if s.HasTeamBindings() {
		enabled, err := s.options.Team.Enabled(scope)
		if err != nil {
			return nil, err
		}
		host.TeamEnabled = enabled
	}
	if s.HasWorkflowBindings() {
		value, e := s.options.Workflow.Guideline(scope)
		if e != nil {
			return nil, e
		}
		host.WorkflowSizeGuideline = value
	}
	if s.HasPlanBindings() {
		value, e := s.options.PlanMode.InterviewPhase()
		if e != nil {
			return nil, e
		}
		host.PlanInterviewPhase = value
	}
	permission, e := s.options.PermissionContext(scope)
	if e != nil {
		return nil, e
	}
	active := []toolschema.AgentPromptDefinition{}
	for _, definition := range host.AgentDefinitions {
		if _, denied := agentDeniedSource(permission, definition.Type); !denied {
			active = append(active, definition)
		}
	}
	host.AgentDefinitions = active
	return host, nil
}

func (s *Service) Call(ctx context.Context, name string, in protocol.JSONObject, scope *cooperative.Scope, userModified bool) (json.RawMessage, error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if e := context.Cause(ctx); e != nil {
		return nil, e
	}
	switch name {
	case "ImageGen", "ImageEdit":
		return s.callImage(ctx, scope, in)
	case "TaskOutput", "TaskStop":
		return s.callBackgroundTask(ctx, scope, name, in, nil)
	case "ListMcpResourcesTool", "ReadMcpResourceTool":
		return s.callMCPResource(ctx, scope, name, in)
	case "EnterPlanMode", "ExitPlanMode":
		return s.callPlanMode(ctx, name, in, scope)
	case "Read":
		return s.read(ctx, in, scope)
	case "Edit":
		return s.edit(ctx, in, scope, userModified)
	case "NotebookEdit":
		return s.notebookEdit(ctx, in, scope)
	case "Glob":
		return s.glob(ctx, in, scope)
	case "Grep":
		return s.grep(ctx, in, scope)
	case "Bash":
		return s.bash(ctx, in, scope)
	case "PowerShell":
		return s.powerShell(ctx, in, scope)
	case "Write":
		return s.options.Write.Call(ctx, in, scope)
	case "TodoWrite":
		return s.todoWrite(scope, in)
	case "AskUserQuestion":
		return questionCall(in), nil
	}
	return nil, capability("tool " + name)
}
