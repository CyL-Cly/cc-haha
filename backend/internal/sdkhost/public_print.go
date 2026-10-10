package sdkhost

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcptools"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/querypreflight"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
)

type PrintOptions struct {
	NativeMainAgentSettings       func(*cooperative.Scope) bool
	PermissionPromptToolName      string
	NativeTypedModelSettings      func(*cooperative.Scope) bool
	StartSandboxNetworkPermission func(context.Context, *cooperative.Scope, queryloop.RegisteredTool, *protocol.JSONObject, queryloop.JSONToolCallContext) (*OwnerFuture[bool], error)
	NativeInteractive             bool
	NativeInputMedia              func(context.Context, *cooperative.Scope, json.RawMessage) (json.RawMessage, []json.RawMessage, error)
	NativeGoalCommand             func(context.Context, *cooperative.Scope, string) (string, bool, error)
	NativeLocalCommand            func(context.Context, *cooperative.Scope, string, string) (string, error)
	NativeSystemContext           func(*cooperative.Scope) (protocol.JSONObject, error)
	NativeContextUsage            func(*cooperative.Scope, bool) (protocol.JSONObject, error)
	OnLocalInputMessages          func([]protocol.JSONObject) error
	AppendSystemPrompt            string
	TeamWorker                    *NativeTeamWorker
	NativeTeamExecution           *NativeTeamExecution
	NativeTeammateExit            *NativeTeammateExit
	NativeTeammateStop            func(context.Context, queryhooks.Context, queryloop.Emit) (queryloop.StopDecision, error)
	NativeLeaderInbox             func(context.Context, *cooperative.Scope, bool) (bool, error)
	FileHistory                   *filehistory.Service
	PreserveToolUseResults        bool
	AgentIteratorReady            func(*cooperative.Scope) error
	AgentRestartPrompt            *NativeAgentRestartPrompt
	ToolSearch                    *toolsearch.Service
	PersistPermission             func(*cooperative.Scope, protocol.JSONObject) error
	PlanProjection                func(*cooperative.Scope) (*attachments.PlanOptions, error)
	PlanAttachments               func(*cooperative.Scope, []queryloop.Message) ([]queryloop.Message, error)
	NativeCloudSettings           func(*cooperative.Scope) bool
	ContextAttachments            func(*cooperative.Scope, []queryloop.Message, *builtinwrite.CallContext) ([]queryloop.Message, error)
	InputAttachments              func(context.Context, *cooperative.Scope, string, []queryloop.Message) ([]queryloop.Message, error)
	AgentID                       string
	QuerySource                   string
	EffortValue                   json.RawMessage
	EffortValueOverridesEnv       bool
	Thinking                      requestcontext.Thinking
	Permissions                   sdkpermissions.Input
	MaxTurns                      int
	Write                         *builtinwrite.Service
	Tools                         *builtintools.Service
	NativeProcess                 bool
	Hooks                         *queryhooks.Service
	Registry                      *PrintRegistry
	ProcessStartMillis            float64
	ResolvePermission             func(context.Context, *cooperative.Scope, queryloop.RegisteredTool, *protocol.JSONObject, queryloop.JSONToolCallContext) (queryloop.ToolPermission, error)
	ResolveRuntimeModel           func(*cooperative.Scope, querypreflight.RuntimeModelInput) (string, error)
	SkillInvocation               func(*cooperative.Scope) (skillcommands.Invocation, error)
	ProcessSkillPrompt            func(context.Context, *cooperative.Scope, string, string, []queryloop.Message, json.RawMessage) (*sessioncontroller.ProcessedInput, error)
	NativeMCP                     *mcptools.Service
	NativeSynthetic               *NativeSyntheticOutput
	NativeJSONSchema              *NativeJSONSchema
	// A non-nil slice is the original resolved Agent tool set. Empty means no
	// tools; nil leaves the main-session catalog unchanged.
	OnlyToolNames *[]string
}

func (h *Host) isAgent() bool {
	return h.options.Print != nil && h.options.Print.AgentID != "" && h.options.Print.TeamWorker == nil
}

func (h *Host) agentID() string {
	if h.options.Print != nil {
		return h.options.Print.AgentID
	}
	return ""
}

func (h *Host) subagentHookID() string {
	if h.isAgent() {
		return h.agentID()
	}
	return ""
}

func (h *Host) querySource() string {
	if h.options.Print != nil && h.options.Print.QuerySource != "" {
		return h.options.Print.QuerySource
	}
	return "sdk"
}

func (h *Host) toolConcurrency() int {
	if h.options.Print != nil && h.options.Print.NativeProcess {
		return queryloop.SourceToolUseConcurrency(h.options.OriginalEnv["CLAUDE_CODE_MAX_TOOL_USE_CONCURRENCY"])
	}
	return 10
}

func originalAgentContext(owner *cooperative.Owner, original *modelaccounting.AgentContextBinding) (*modelaccounting.AgentContextBinding, error) {
	if original == nil {
		return modelaccounting.NewMainContext(owner)
	}
	if !original.Owns(owner) {
		return nil, cap("original agent context owner identity")
	}
	return original, nil
}

func NewOwnedPrint(scope *cooperative.Scope, o Options) (*Host, error) {
	if o.Print == nil || !scope.BelongsTo(o.Owner) || o.Print.Write == nil || !o.Print.Write.Owns(o.Owner) || o.Print.Hooks == nil || o.Print.Permissions.Profile != sdkpermissions.PublicPrintProfile || o.Print.MaxTurns < 1 || o.Print.Registry == nil {
		return nil, cap("public print native tool/permission/max-turns bindings")
	}
	if !o.Startup.SharesSettings(o.Settings, o.SettingsState, o.Environment) || !o.Controller.UsesOriginalSettingsContext(o.Startup, o.Settings, o.SettingsState, o.Environment) {
		return nil, cap("shared print settings construction identity")
	}
	if o.Print.FileHistory != nil && !o.Controller.UsesNativeFileHistory(o.Print.FileHistory) {
		return nil, cap("actual controller file history construction identity")
	}
	if e := validateNeutralEnvironment(o.Environment, o.OriginalEnv); e != nil {
		return nil, e
	}
	return newHost(scope, o)
}
func (h *Host) initializePrintCatalog(scope *cooperative.Scope) error {
	defs, registered, e := h.options.Print.Write.Catalog(scope)
	if h.options.Print.Tools != nil {
		defs, registered, e = h.options.Print.Tools.Catalog(scope)
	}
	if e != nil {
		return e
	}
	if h.options.Print.NativeMCP != nil {
		extra, tools, err := h.options.Print.NativeMCP.Catalog(scope)
		if err != nil {
			return err
		}
		defs = append(defs, extra...)
		registered = append(registered, tools...)
		h.mcpServers, err = h.options.Print.NativeMCP.Status(scope)
		if err != nil {
			return err
		}
	}
	if b := h.options.Print.NativeSynthetic; b != nil {
		if !b.Owns(h.options.Owner) {
			return cooperative.ErrRestricted
		}
		definition, tool, e := b.catalog(scope)
		if e != nil {
			return e
		}
		defs = append(defs, definition)
		registered = append(registered, tool)
	}
	names := h.options.Print.OnlyToolNames
	if team := h.options.Print.TeamWorker; team != nil {
		if err := team.check(h, scope); err != nil {
			return err
		}
		if team.tools != nil {
			names = &team.tools
		}
	}
	if names != nil {
		selectedDefinitions := defs[:0:0]
		selectedTools := registered[:0:0]
		seen := map[string]bool{}
		for _, name := range *names {
			if seen[name] {
				continue
			}
			seen[name] = true
			for _, definition := range defs {
				if definition.Name == name {
					selectedDefinitions = append(selectedDefinitions, definition)
					break
				}
			}
			for _, tool := range registered {
				if tool.Name == name {
					selectedTools = append(selectedTools, tool)
					break
				}
			}
		}
		defs, registered = selectedDefinitions, selectedTools
	}
	if h.options.Print.NativeProcess {
		defs, registered, e = mergeNativeToolPool(defs, registered)
		if e != nil {
			return e
		}
	}
	h.definitions, h.registered = defs, registered
	if e := h.captureToolSearchCatalogue(scope); e != nil {
		return e
	}
	h.originalTools = []protocol.JSONObject{}
	for _, d := range defs {
		h.originalTools = append(h.originalTools, protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: quote(d.Kind)}, protocol.JSONField{Name: "name", Value: quote(d.Name)}))
	}
	if h.options.Print.NativeSynthetic != nil {
		if schema := h.options.Print.NativeJSONSchema; schema != nil && schema.Synthetic() == h.options.Print.NativeSynthetic {
			return h.options.Controller.InstallNativePublicJSONSchemaTools(scope, h.originalTools, h.options.Print.NativeMCP, schema)
		}
		return h.options.Controller.InstallNativeSyntheticPrintTools(scope, h.originalTools, h.options.Print.NativeMCP, h.options.Print.NativeSynthetic)
	}
	if h.options.Print.NativeMCP != nil {
		return h.options.Controller.InstallNativeMCPPrintTools(scope, h.originalTools, h.options.Print.NativeMCP)
	}
	return h.options.Controller.InstallPrintTools(scope, h.originalTools)
}
func (h *Host) maxTurns() int {
	if h.options.Print != nil {
		return h.options.Print.MaxTurns
	}
	return 1
}
func publicPrintRequestEnvironment(original map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range original {
		switch k {
		case "ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "CLAUDE_CODE_DISABLE_FAST_MODE", "CLAUDE_CODE_MAX_RETRIES", "NODE_EXTRA_CA_CERTS", "API_TIMEOUT_MS", "CLAUDE_CODE_SIMPLE":
			out[k] = v
		case "CLAUDE_CODE_ENTRYPOINT":
			if v != "sdk-cli" {
				return nil, cap("normal print entrypoint")
			}
			out[k] = v
		case "HOME", "USERPROFILE", "CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "TMPDIR", "PWD", "PATH", "LANG", "TZ", "NODE_ENV", "USER_TYPE", "BUN_OPTIONS", "COREPACK_ENABLE_AUTO_PIN", "NoDefaultCurrentDirectoryInExePath", "USE_BUILTIN_RIPGREP", "NO_COLOR", "TEMP", "TMP", "APPDATA", "LOCALAPPDATA", "XDG_DATA_HOME", "XDG_STATE_HOME", "GH_CONFIG_DIR", "CC_HAHA_FIXTURE_TEMP_ROOT": // retained original native/process inputs, not request options
		case "GOCOVERDIR", "TEST_TELEMETRY_DIR":
			// Go runtime/tool process inputs remain in OriginalEnv. They are
			// neither Source feature controls nor provider request options.
		case "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC":
			if v != "1" {
				return nil, cap("normal print external telemetry transport")
			}
		case "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX", "CLAUDE_ENABLE_STREAM_WATCHDOG": // actual producer policy consumer
		default:
			return nil, cap("normal print original environment " + k)
		}
	}
	return out, nil
}

// NativePrintRequestEnvironment projects only actual provider/request controls
// from the original OS environment. Shell, PATH and cache inputs remain on the
// process graph and do not become request fields or finite-profile refusals.
func NativePrintRequestEnvironment(original map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for name, value := range original {
		if requestcontext.IsNativePolicyEnvironment(name) || requestretry.IsOpenAIClientEnvironment(name) || requestretry.IsGrokClientEnvironment(name) || costAuthEnvTruthy(original["CLAUDE_CODE_USE_BEDROCK"]) && requestcontext.IsNativeBedrockEnvironment(name) || costAuthEnvTruthy(original["CLAUDE_CODE_USE_VERTEX"]) && requestcontext.IsNativeVertexEnvironment(name) {
			out[name] = value
		}
	}
	for _, key := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_CUSTOM_HEADERS", "ANTHROPIC_DEFAULT_HEADERS", "CLAUDE_CODE_DISABLE_FAST_MODE", "CLAUDE_CODE_MAX_RETRIES", "NODE_EXTRA_CA_CERTS", "API_TIMEOUT_MS", "CLAUDE_CODE_SIMPLE", "CLAUDE_CODE_ENTRYPOINT", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		if value, ok := original[key]; ok {
			out[key] = value
		}
	}
	// Source getProxyUrl only reads HTTP(S)_PROXY and NO_PROXY. ALL_PROXY
	// remains in OriginalEnv/the actual tool process, but is not an Anthropic
	// model-client option or routing input.
	delete(out, "ALL_PROXY")
	delete(out, "all_proxy")
	return out, nil
}
func nativePreflightEnvironment(name string) bool {
	switch name {
	case "CLAUDE_CODE_MODEL_CONTEXT_WINDOWS", "CLAUDE_CODE_MAX_OUTPUT_TOKENS", "CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS", "ANTHROPIC_DEFAULT_FABLE_MODEL", "CLAUDE_CODE_ENTRYPOINT", "DISABLE_COMPACT", "DISABLE_AUTO_COMPACT", "CLAUDE_CODE_BLOCKING_LIMIT_OVERRIDE", "CLAUDE_CODE_AUTO_COMPACT_WINDOW", "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", "ANTHROPIC_MODEL", "USER_TYPE", "CLAUDE_INTERNAL_FC_OVERRIDES", "CLAUDE_CODE_DISABLE_1M_CONTEXT", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL":
		return true
	default:
		return false
	}
}
func (h *Host) installPrintBatch(scope *cooperative.Scope, loop *queryloop.Host, mainLoopModel string, hookContext func(queryloop.Round) queryhooks.Context) error {
	hooks := h.options.Print.Hooks
	h.nativePermissionHookContext = hookContext
	sid := h.options.Controller.SessionID
	constructor := queryloop.NewSerialToolBatch
	if h.options.Print.Tools != nil {
		constructor = queryloop.NewToolBatch
	}
	var preEffects func(context.Context, queryloop.RegisteredTool, *protocol.JSONObject, queryloop.JSONToolCallContext, func(queryloop.Message) error) (queryloop.PreToolEffects, error)
	var postEffects func(context.Context, queryloop.RegisteredTool, *protocol.JSONObject, queryloop.ToolCallResult, queryloop.JSONToolCallContext, func(queryloop.Message) error) (queryloop.PostToolEffects, error)
	var failureEffects func(context.Context, queryloop.RegisteredTool, *protocol.JSONObject, error, queryloop.JSONToolCallContext) ([]queryloop.Message, error)
	if h.options.Print.NativeProcess && hooks.HasNativeCommandBindings(h.options.Owner) {
		preEffects = func(ctx context.Context, tool queryloop.RegisteredTool, in *protocol.JSONObject, call queryloop.JSONToolCallContext, progress func(queryloop.Message) error) (queryloop.PreToolEffects, error) {
			return h.nativePreToolHooks(ctx, scope, tool, in, call, hookContext, progress)
		}
		postEffects = func(ctx context.Context, tool queryloop.RegisteredTool, in *protocol.JSONObject, out queryloop.ToolCallResult, call queryloop.JSONToolCallContext, progress func(queryloop.Message) error) (queryloop.PostToolEffects, error) {
			return h.nativePostToolHooks(ctx, scope, tool, in, out, call, hookContext, progress)
		}
		failureEffects = func(ctx context.Context, tool queryloop.RegisteredTool, in *protocol.JSONObject, failure error, call queryloop.JSONToolCallContext) ([]queryloop.Message, error) {
			return h.nativeFailureToolHooks(ctx, scope, tool, in, failure, call, hookContext)
		}
	}
	batch, e := constructor(h.registered, queryloop.ToolBatchCapabilities{OmitSuccessfulToolUseResult: h.isAgent() && !h.options.Print.PreserveToolUseResults, OmitMCPMeta: h.isAgent(), Owner: h.options.Owner, Scope: scope, MaxConcurrency: h.toolConcurrency(), JSON: &queryloop.JSONToolBatchCapabilities{
		PreToolEffects:     preEffects,
		PostToolEffects:    postEffects,
		FailureToolEffects: failureEffects,
		PreTool: func(_ context.Context, _ queryloop.RegisteredTool, in *protocol.JSONObject, _ queryloop.JSONToolCallContext) (*protocol.JSONObject, error) {
			if e := hooks.EmptyEvent("PreToolUse", sid()); e != nil {
				return nil, e
			}
			if e := scope.AwaitFulfilled(); e != nil {
				return nil, e
			}
			return in, nil
		},
		ResolvePermission: func(ctx context.Context, tool queryloop.RegisteredTool, in *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
			return h.resolvePrintToolPermission(ctx, scope, tool, in, call)
		},
		PostTool: func(_ context.Context, _ queryloop.RegisteredTool, _ *protocol.JSONObject, _ queryloop.ToolCallResult, _ queryloop.JSONToolCallContext) ([]queryloop.Message, error) {
			if e := hooks.EmptyEvent("PostToolUse", sid()); e != nil {
				return nil, e
			}
			return nil, scope.AwaitFulfilled()
		},
		// Source QueryEngine/queryContext's headless setInProgressToolUseIDs is a noop.
		InProgress: func(json.RawMessage, bool) error { return nil },
	}, DeprecatedCatalog: []queryloop.ToolDescriptor{}, CancellationContent: quote("[Request interrupted by user]")}, h.options.Factory)
	if e != nil {
		return e
	}
	loop.LookupBackfillJSON = batch.LookupBackfillJSON
	loop.LookupBackfill = nil
	loop.ExecuteBatch = func(ctx context.Context, round queryloop.Round, uses []queryloop.Invocation, emit func(queryloop.Message) error) (any, error) {
		original := round.ToolContext
		currentModel, e := h.skillMainModel(mainLoopModel)
		if e != nil {
			return nil, e
		}
		actualMessages, ok := original.([]queryloop.Message)
		if !ok {
			return nil, cap("actual tool-context message history")
		}
		round.ToolContext = &builtinwrite.CallContext{ToolSearchCatalogue: h.toolSearchCatalogue, Scope: scope, AgentID: h.agentID(), MainLoopModel: currentModel, SkillOverrides: h.askSkillOverrides, Messages: actualMessages, PermissionContext: h.NativePermissionContext}
		updated, e := batch.Execute(ctx, round, uses, emit)
		if e == nil {
			e = h.captureSkillCallOverrides(updated)
		}
		return original, e
	}
	loop.NormalizeToolResult = func(message queryloop.Message) ([]queryloop.Message, error) {
		enabled := h.options.Print.ToolSearch != nil && h.toolSearchCatalogue != nil && toolsearch.Optimistic(h.toolSearchCatalogue.Gate.Environment)
		values, e := queryloop.NormalizeToolResultWithSearch(message, enabled)
		if e != nil || !enabled {
			return values, e
		}
		names := []json.RawMessage{}
		for _, tool := range h.toolSearchCatalogue.Tools {
			names = append(names, tool.Name)
		}
		for i, value := range values {
			record, e := requestcontext.FilterUnavailableToolReferences(value.Record, names)
			if e != nil {
				return nil, e
			}
			values[i].Record = requestcontext.EnsureToolReferenceTurnBoundary(record)
		}
		return values, nil
	}
	// The successful Team submit boundary runs only after every result in the
	// real tool batch has been yielded. It starts no approval or member itself.
	loop.PauseAfterTools = func(ctx context.Context, _ queryloop.Round) (bool, error) {
		if h.options.Print.Tools == nil {
			return false, nil
		}
		return h.options.Print.Tools.ConsumeTeamPlanTurnPause(ctx, scope)
	}
	return nil
}
