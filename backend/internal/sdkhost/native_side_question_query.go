package sdkhost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/apipresentation"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/querypreflight"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/sidequestion"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// querySideQuestion uses the same query/model/credential services. The fork
// has its own mutable history and abort context, without a second Controller.
func (h *Host) querySideQuestion(ctx context.Context, caller *cooperative.Scope, snapshot nativeSideQuestionCache) (protocol.JSONObject, error) {
	if !caller.BelongsTo(h.options.Owner) || !h.nativeSideQuestionModels.Current(caller) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	var answer protocol.JSONObject
	err := func() error {
		scope := caller
		var random [8]byte
		if _, e := rand.Read(random[:]); e != nil {
			return e
		}
		agentID := "a" + hex.EncodeToString(random[:])
		input := snapshot.request
		deps := h.requestDependencies(scope)
		deps.NativeSideQuestionSource = h.nativeSideQuestionModels
		// Cache-safe values are copied, but owner-bound credential factories must
		// be issued for this real fork continuation, never its completed parent.
		input.Context.Host.NativeCodexOAuth = deps.NativeCodexOAuth
		if deps.NativeCodexOAuth != nil {
			deps.NativeCodexOAuth.AgentID = raw(agentID)
			deps.NativeCodexOAuth.TurnState = providerproxy.NewOpenAICodexTurnState(ctx)
			defer deps.NativeCodexOAuth.TurnState.Dispose()
		}
		input.Context.Host.NativeGrokOAuth = deps.NativeGrokOAuth
		env := map[string]string{}
		for key, value := range h.options.OriginalEnv {
			if nativePreflightEnvironment(key) {
				env[key] = value
			}
		}
		var decision querypreflight.Result
		stopActive := false
		contextFor := func(round queryloop.Round, assistants []*streamhistory.Envelope) queryhooks.Context {
			history, _ := round.ToolContext.([]queryloop.Message)
			actual := &builtinwrite.CallContext{Scope: scope, AgentID: agentID, MainLoopModel: input.Context.Options.Model, Messages: history, ToolSearchCatalogue: input.Context.Host.ToolSearchCatalogue, PermissionContext: func(current *cooperative.Scope) (protocol.JSONObject, error) {
				value, e := h.NativePermissionContext(current)
				if e == nil {
					value = value.Clone()
					value.Set("shouldAvoidPermissionPrompts", raw(true))
				}
				return value, e
			}}
			return queryhooks.Context{Scope: scope, AgentID: agentID, StopHookActive: stopActive, History: history, Assistants: assistants, SystemPrompt: decision.SystemPrompt, UserContext: systemContextStrings(snapshot.raw.UserContext), SystemContext: systemContextStrings(snapshot.systemContext), ToolUseContext: actual, QuerySource: "side_question", ComputerUseInactive: true, GoalInactive: true, TeammateInactive: true}
		}
		var firstAPIError error
		loop := queryloop.Host{OnNativeQuota: h.nativeQuotaConsumer(), RetryAPIError: func(value error) {
			if firstAPIError == nil {
				firstAPIError = value
			}
		}, Activity: h.options.Activity, Session: h.sessionAccounting, AgentContext: h.agentContext, StreamClock: h.streamClock, StreamPolicy: h.streamPolicy, SuccessDuration: &modelaccounting.SuccessDuration{NowMillis: h.options.NowMillis, GetPermission: h.options.Controller.SuccessPermissionOnOwner, Ignored: h.permissionRejections}, Request: deps,
			ResolvePreflight: func(ctx context.Context, round queryloop.Round) (queryloop.PreflightResult, error) {
				var e error
				decision, e = h.preflight.Run(ctx, querypreflight.Input{SDKBetas: h.options.SDKBetas, NativeClaudeProof: h.nativeClaudeProof(scope), Profile: querypreflight.NativeProcessProfile, History: round.History, SystemPrompt: snapshot.raw.Context.SystemPrompt, UserContext: snapshot.raw.UserContext, SystemContext: snapshot.systemContext, MainLoopModel: input.Context.Options.Model, QuerySource: "side_question", PermissionMode: h.permissionMode, OriginalEnvironment: env, ToolCount: len(h.definitions), Tools: h.definitions, PlanOptions: input.Context.Host.PlanOptions})
				out := queryloop.PreflightResult{History: decision.ModelHistory, ToolContext: decision.ToolContextHistory, CurrentModel: decision.CurrentModel}
				if decision.Blocked != nil {
					out.Terminal = &queryloop.PreflightTerminal{Message: *decision.Blocked, Reason: "blocking_limit"}
				}
				return out, e
			}, ResolveModelInput: func(_ context.Context, round queryloop.Round) (requestretry.Input, error) {
				history := []protocol.JSONObject{}
				for _, message := range round.History {
					data, e := message.MarshalJSON()
					if e != nil {
						return requestretry.Input{}, e
					}
					var row protocol.JSONObject
					if e = row.UnmarshalJSON(data); e != nil {
						return requestretry.Input{}, e
					}
					history = append(history, row)
				}
				request := input
				projected, e := h.serial.Resolve(context.Background(), snapshot.raw, history)
				if e != nil {
					return requestretry.Input{}, e
				}
				request.Context.History = projected.History
				request.Context.SystemPrompt = decision.SystemPrompt
				request.Context.Options.Model = decision.CurrentModel
				request, e = h.bindSideQuestionFastMode(scope, request)
				if e != nil {
					return requestretry.Input{}, e
				}
				if h.options.ObserveModelInput != nil {
					h.options.ObserveModelInput(request)
				}
				return request, nil
			}, PostSampling: func(round queryloop.Round, a []*streamhistory.Envelope) error {
				_, e := h.hooks.StartPostSampling(contextFor(round, a))
				return e
			}, StopFailure: func(round queryloop.Round, a *streamhistory.Envelope) error {
				_, e := h.hooks.StartStopFailure(contextFor(round, nil), a)
				return e
			}, AbortCleanup: func(ctx context.Context, round queryloop.Round) error {
				return h.hooks.AbortCleanup(ctx, contextFor(round, nil))
			}, StopHooks: func(ctx context.Context, round queryloop.Round, a []*streamhistory.Envelope, emit queryloop.Emit) (queryloop.StopDecision, error) {
				result, e := h.hooks.StopHooks(ctx, contextFor(round, a), emit)
				if e == nil && len(result.BlockingMessages) > 0 && !result.PreventContinuation {
					stopActive = true
				}
				return result, e
			},
		}
		if e := h.installPrintBatch(scope, &loop, input.Context.Options.Model, func(round queryloop.Round) queryhooks.Context { return contextFor(round, nil) }); e != nil {
			return e
		}
		initial := []queryloop.Message{}
		for _, row := range input.Context.History {
			initial = append(initial, queryloop.Message{Record: row})
		}
		tracking := queryloop.Tracking{ChainID: h.options.UUID(), Depth: -1}
		if snapshot.tracking != nil {
			tracking.Depth = snapshot.tracking.Depth
		}
		// RunControlled advances an inherited tracking chain at the first round.

		records := []protocol.JSONObject{}
		_, e := queryloop.RunControlled(ctx, initial, queryloop.Options{Profile: queryloop.SuccessfulToolRound, Model: input.Context.Options.Model, QuerySource: "side_question", NonInteractive: true, MaxTurns: 1, InitialTracking: &tracking, UUID: h.options.UUID, Now: h.options.Now}, loop, func(yield queryloop.Yield) (requestretry.NoticeAction, error) {
			if yield.Message != nil {
				data, e := yield.Message.MarshalJSON()
				if e != nil {
					return 0, e
				}
				var row protocol.JSONObject
				if e = row.UnmarshalJSON(data); e != nil {
					return 0, e
				}
				if e := sidequestion.CheckMessage(row); e != nil {
					return 0, e
				}
				records = append(records, row)
			}
			return requestretry.Resume, nil
		})
		if e != nil {
			return e
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		var apiErrorMessage json.RawMessage
		if firstAPIError != nil {
			var e error
			apiErrorMessage, e = apipresentation.FormatAPIErrorText(firstAPIError)
			if e != nil {
				return e
			}
		}
		text, e := sidequestion.ExtractWithAPIError(records, apiErrorMessage)
		if e != nil {
			return e
		}
		answer = protocol.NewJSONObject(protocol.JSONField{Name: "response", Value: text})
		return nil
	}()
	return answer, err
}
