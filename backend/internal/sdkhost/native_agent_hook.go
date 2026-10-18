package sdkhost

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/agenthook"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/querypreflight"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

// QueryAgentHook is Source's direct query() consumer. It does not construct a
// second MainPrint/Owner/Controller, emit SDK ACKs, or Record auxiliary history.
func (h *Host) QueryAgentHook(ctx context.Context, caller *cooperative.Scope, input queryhooks.AgentModelInput, receive func(queryloop.Message) (bool, error)) error {
	if h == nil || !caller.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.hooks.HasNativeAgentBindings(h.options.Owner) || !h.nativeAgentHookModels.Owns(h.options.Owner) || receive == nil {
		return cooperative.ErrRestricted
	}
	original, ok := input.ToolUseContext.(*builtinwrite.CallContext)
	if !ok || original == nil || !original.Scope.BelongsTo(h.options.Owner) {
		return cap("Agent Hook actual ToolUseContext")
	}
	return h.nativeAgentHookModels.Run(caller, ctx, func(scope *cooperative.Scope) error {
		return h.queryAgentHookOnProducer(ctx, scope, input, original, receive)
	})
}

func (h *Host) queryAgentHookView(scope *cooperative.Scope, input queryhooks.AgentModelInput, parent *builtinwrite.CallContext) (*Host, error) {
	print := *h.options.Print
	print.AgentID, print.QuerySource, print.Thinking = input.HookAgentID, "hook_agent", requestcontext.Thinking{Type: "disabled"}
	print.AgentIteratorReady, print.AgentRestartPrompt, print.TeamWorker, print.NativeTeammateStop, print.NativeTeammateExit, print.NativeTeamExecution = nil, nil, nil, nil, nil, nil
	print.NativeLeaderInbox, print.ResolvePermission = nil, nil
	options := h.options
	options.Print = &print
	options.Model = input.Model
	options.OnQueryYield = nil
	view := &Host{options: options, hooks: h.hooks, preflight: h.preflight, sessionAccounting: h.sessionAccounting, agentContext: h.agentContext, requestEnvironment: h.requestEnvironment, permissionRejections: h.permissionRejections, streamClock: h.streamClock, streamPolicy: h.streamPolicy, toolSchemas: h.toolSchemas, permissionMode: "dontAsk", nativeAgentHookModels: h.nativeAgentHookModels}
	if parent.SkillOverrides != nil {
		copy := parent.SkillOverrides.Clone()
		copy.Delete("model")
		view.askSkillOverrides = &copy
	}
	view.nativePermissionView = func(current *cooperative.Scope) (protocol.JSONObject, error) {
		actual, e := h.NativePermissionContext(current)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		return agenthook.PermissionContext(actual, input.TranscriptPath)
	}
	get := view.NativePermissionContext
	var e error
	if print.Tools != nil {
		print.Tools, e = print.Tools.PermissionView(scope, get)
		if e != nil {
			return nil, e
		}
	}
	print.Write, e = print.Write.PermissionView(scope, get)
	if e != nil {
		return nil, e
	}
	if print.NativeMCP != nil {
		print.NativeMCP, e = print.NativeMCP.PermissionView(scope, get)
		if e != nil {
			return nil, e
		}
	}
	allowed := map[string]bool{}
	for _, d := range h.definitions {
		if agenthook.ToolAllowed(d.Name, h.options.OriginalEnv["USER_TYPE"]) {
			view.definitions = append(view.definitions, d)
			allowed[d.Name] = true
		}
	}
	for _, tool := range h.registered {
		if allowed[tool.Name] {
			view.registered = append(view.registered, tool)
		}
	}
	for _, tool := range h.originalTools {
		if allowed[tool.String("name")] {
			view.originalTools = append(view.originalTools, tool.Clone())
		}
	}
	native, e := h.options.Controller.BorrowNativeCopyBinding(h.options.Owner)
	if e != nil {
		return nil, e
	}
	synthetic, e := NewNativeSyntheticOutput(h.options.Owner, native, agenthook.StructuredSchema, func(_ context.Context, input protocol.JSONObject) error {
		_, err := toolschema.ParseAgentHookOutput(input)
		return err
	})
	if e != nil {
		return nil, e
	}
	print.NativeSynthetic = synthetic
	definition, tool, e := synthetic.catalog(scope)
	if e != nil {
		return nil, e
	}
	definition.Prompt = quote(agenthook.StructuredPrompt)
	tool.ParseInput = toolschema.ParseAgentHookOutput
	view.definitions = append(view.definitions, definition)
	view.registered = append(view.registered, tool)
	view.originalTools = append(view.originalTools, protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: quote("registered")}, protocol.JSONField{Name: "name", Value: quote("StructuredOutput")}))
	view.askRunning = true
	view.askInferenceModel = input.Model
	if e = view.captureToolSearchCatalogue(scope); e != nil {
		return nil, e
	}
	return view, nil
}

func (h *Host) queryAgentHookOnProducer(ctx context.Context, scope *cooperative.Scope, input queryhooks.AgentModelInput, parent *builtinwrite.CallContext, receive func(queryloop.Message) (bool, error)) error {
	view, e := h.queryAgentHookView(scope, input, parent)
	if e != nil {
		return e
	}
	if e = h.hooks.RegisterAgentStructuredOutput(scope, input.HookAgentID); e != nil {
		return e
	}
	deviceID, e := h.requestDeviceID(scope)
	if e != nil {
		return e
	}
	defs := append([]toolschema.Definition{}, view.definitions...)
	requestHost := requestcontext.Host{NativeToolSearch: view.options.Print.ToolSearch, ToolSearchCatalogue: view.toolSearchCatalogue, Profile: requestcontext.NativeDirectRelayProfile, Env: h.requestEnvironment, DeviceID: deviceID, SessionID: h.options.Controller.SessionID(), NativeToolDefinitions: &defs, SDKBetas: h.options.SDKBetas, Workload: h.options.Workload, TTLAllowlist: []string{}, ToolSchemas: h.toolSchemas}
	if view.options.Print.Tools != nil {
		requestHost.ToolSchemaHost, e = view.options.Print.Tools.SchemaHostForScope(scope, input.Model, h.options.OriginalEnv["ANTHROPIC_BASE_URL"])
		if e != nil {
			return e
		}
	}
	if view.options.Print.PlanProjection != nil {
		requestHost.PlanOptions, e = view.options.Print.PlanProjection(scope)
		if e != nil {
			return e
		}
	}
	env := map[string]string{}
	for name, value := range h.options.OriginalEnv {
		if nativePreflightEnvironment(name) {
			env[name] = value
		}
	}
	system := []json.RawMessage{quote(input.SystemPrompt)}
	var decision querypreflight.Result
	stopActive := false
	hookContext := func(round queryloop.Round, a []*streamhistory.Envelope) queryhooks.Context {
		history, _ := round.ToolContext.([]queryloop.Message)
		model, _ := view.skillMainModel(input.Model)
		actual := &builtinwrite.CallContext{Scope: scope, AgentID: input.HookAgentID, MainLoopModel: model, SkillOverrides: view.askSkillOverrides, Messages: history, ToolSearchCatalogue: view.toolSearchCatalogue, PermissionContext: view.NativePermissionContext}
		return queryhooks.Context{Scope: scope, StopHookActive: stopActive, History: history, Assistants: a, SystemPrompt: decision.SystemPrompt, UserContext: map[string]string{}, SystemContext: map[string]string{}, ToolUseContext: actual, QuerySource: "hook_agent", AgentID: input.HookAgentID, ComputerUseInactive: true, GoalInactive: true, TeammateInactive: true}
	}
	deps := view.requestDependencies(scope)
	deps.NativeAgentHookSource = h.nativeAgentHookModels
	loop := queryloop.Host{OnNativeQuota: h.nativeQuotaConsumer(), Activity: h.options.Activity, Session: h.sessionAccounting, AgentContext: h.agentContext, StreamClock: h.streamClock, StreamPolicy: h.streamPolicy, SuccessDuration: &modelaccounting.SuccessDuration{NowMillis: h.options.NowMillis, GetPermission: h.options.Controller.SuccessPermissionOnOwner, Ignored: h.permissionRejections}, Request: deps,
		ResolvePreflight: func(ctx context.Context, round queryloop.Round) (queryloop.PreflightResult, error) {
			model, e := view.skillMainModel(input.Model)
			if e != nil {
				return queryloop.PreflightResult{}, e
			}
			opts := querypreflight.Input{SDKBetas: h.options.SDKBetas, NativeClaudeProof: h.nativeClaudeProof(scope), Profile: querypreflight.NativeProcessProfile, History: round.History, SystemPrompt: system, UserContext: protocol.NewJSONObject(), SystemContext: protocol.NewJSONObject(), MainLoopModel: model, QuerySource: "hook_agent", PermissionMode: "dontAsk", OriginalEnvironment: env, ToolCount: len(view.definitions), Tools: view.definitions, PlanOptions: requestHost.PlanOptions}
			if view.options.Print.ResolveRuntimeModel != nil {
				opts.ResolveRuntimeModel = func(in querypreflight.RuntimeModelInput) (string, error) {
					return view.options.Print.ResolveRuntimeModel(scope, in)
				}
			}
			var e2 error
			decision, e2 = h.preflight.Run(ctx, opts)
			out := queryloop.PreflightResult{History: decision.ModelHistory, ToolContext: decision.ToolContextHistory, CurrentModel: decision.CurrentModel}
			if decision.Blocked != nil {
				out.Terminal = &queryloop.PreflightTerminal{Message: *decision.Blocked, Reason: "blocking_limit"}
			}
			return out, e2
		},
		ResolveModelInput: func(_ context.Context, round queryloop.Round) (requestretry.Input, error) {
			history := []protocol.JSONObject{}
			for _, message := range round.History {
				data, e := message.MarshalJSON()
				if e != nil {
					return requestretry.Input{}, e
				}
				var record protocol.JSONObject
				if e = record.UnmarshalJSON(data); e != nil {
					return requestretry.Input{}, e
				}
				history = append(history, record)
			}
			opts := requestcontext.Options{Model: decision.CurrentModel, QuerySource: "hook_agent", IsNonInteractiveSession: true, EffortValue: printEffort(view.options.Print)}
			if view.askSkillOverrides != nil && view.askSkillOverrides.Get("effort") != nil {
				opts.EffortValue = append(json.RawMessage(nil), view.askSkillOverrides.Get("effort")...)
				opts.EffortValueOverridesEnv = true
			}
			disabled := false
			req := requestretry.Input{Context: requestcontext.Context{History: history, SystemPrompt: decision.SystemPrompt, Tools: view.originalTools, Thinking: requestcontext.Thinking{Type: "disabled"}, Options: opts, Host: requestHost}, Runtime: h.options.Runtime, ResolvedFastMode: &disabled}
			if h.options.ObserveModelInput != nil {
				h.options.ObserveModelInput(req)
			}
			return req, nil
		},
		PostSampling: func(round queryloop.Round, a []*streamhistory.Envelope) error {
			_, e := h.hooks.StartPostSampling(hookContext(round, a))
			return e
		},
		StopFailure: func(round queryloop.Round, a *streamhistory.Envelope) error {
			_, e := h.hooks.StartStopFailure(hookContext(round, nil), a)
			return e
		},
		AbortCleanup: func(ctx context.Context, round queryloop.Round) error {
			return h.hooks.AbortCleanup(ctx, hookContext(round, nil))
		},
		StopHooks: func(ctx context.Context, round queryloop.Round, a []*streamhistory.Envelope, emit queryloop.Emit) (queryloop.StopDecision, error) {
			result, e := h.hooks.StopHooks(ctx, hookContext(round, a), emit)
			if e == nil && len(result.BlockingMessages) > 0 && !result.PreventContinuation {
				stopActive = true
			}
			return result, e
		},
	}
	if e = view.installPrintBatch(scope, &loop, input.Model, func(round queryloop.Round) queryhooks.Context { return hookContext(round, nil) }); e != nil {
		return e
	}
	if h.options.Print.ContextAttachments != nil {
		loop.AttachmentsAfterTools = func(_ context.Context, round queryloop.Round) ([]queryloop.Message, error) {
			actual := hookContext(round, nil).ToolUseContext.(*builtinwrite.CallContext)
			return h.options.Print.ContextAttachments(scope, round.History, actual)
		}
	} else if h.options.Print.PlanAttachments != nil {
		return cap("actual Agent Hook scoped attachments")
	}
	active, cancel := context.WithCancel(ctx)
	defer cancel()
	_, e = queryloop.RunControlled(active, []queryloop.Message{{Record: input.Prompt.Clone()}}, queryloop.Options{Profile: queryloop.SuccessfulToolRound, Model: input.Model, QuerySource: "hook_agent", NonInteractive: true, MaxTurns: 50, UUID: h.options.UUID, Now: h.options.Now}, loop, func(value queryloop.Yield) (requestretry.NoticeAction, error) {
		if value.RequestStart {
			return requestretry.Resume, nil
		}
		var message queryloop.Message
		if value.Message != nil {
			message = *value.Message
		} else if value.Stream != nil {
			message = queryloop.Message{Record: protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("stream_event")}, protocol.JSONField{Name: "event", Value: value.Stream.Event})}
		} else {
			return 0, errors.New("Agent Hook query returned no original yield")
		}
		next, e := receive(message)
		if e != nil {
			return 0, e
		}
		if !next {
			cancel()
			return requestretry.Stop, nil
		}
		return requestretry.Resume, nil
	})
	return e
}

func printEffort(print *PrintOptions) json.RawMessage {
	if print == nil {
		return nil
	}
	return append(json.RawMessage(nil), print.EffortValue...)
}
