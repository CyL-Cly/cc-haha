package sdkhost

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"time"
)

func (h *Host) requestDependencies(scope *cooperative.Scope) requestretry.Dependencies {
	var bedrock *requestretry.NativeBedrock
	var cloudError error
	parentClient, parentClientError := h.options.Controller.NativeParentClientStateOnOwner(scope)
	if h.options.NativeBedrock != nil {
		bedrock, cloudError = h.options.NativeBedrock(scope)
	}
	var vertex *requestretry.NativeVertex
	if h.options.NativeVertex != nil {
		var err error
		vertex, err = h.options.NativeVertex(scope)
		if err != nil {
			cloudError = err
		}
	}
	if parentClientError != nil {
		cloudError = parentClientError
	}
	var codex *provider.OpenAICodexConfig
	if h.options.NativeOpenAI != nil {
		codex = h.options.NativeOpenAI(scope)
	}
	var grok *provider.GrokOAuthConfig
	if h.options.NativeGrok != nil {
		grok = h.options.NativeGrok(scope)
	}
	var claude *requestretry.NativeClaudeAuth
	var claudeError error
	if h.options.NativeClaudeAuth != nil {
		claude, claudeError = h.options.NativeClaudeAuth(scope)
	}
	var helper *requestretry.NativeAuthHelper
	if h.options.NativeAuthHelper != nil {
		helper, _ = h.options.NativeAuthHelper(scope)
	}
	fast, fastError := h.nativeFastModeBinding(scope)
	var proof *cooperative.NativeFastModeRequest
	var state *retrypolicy.FastState
	var effect func(retrypolicy.FastEffect) error
	if fast != nil {
		proof, state, effect = fast.Proof, fast.State, fast.Effect
	}
	return requestretry.Dependencies{ObserveOpenFailure: h.options.ObserveModelFailure, NativeThinkingSummaryBeta: h.nativeThinkingSummaryBetaCallback(), NativeFastModeProof: proof, NativeFastState: state, NativeFastEffect: effect, NativeFastError: fastError, NativeVertex: vertex, NativeBedrock: bedrock, NativeCloudError: cloudError, NativeAuthHelper: helper, NativeClaudeAuth: claude, NativeClaudeError: claudeError, NativeCodexOAuth: codex, NativeGrokOAuth: grok, PromptSnapshot: h.promptSnapshotObserver(scope), Trace: h.options.Trace, TraceEnvironment: h.options.OriginalEnv, StreamOwner: h.options.Owner, StreamScope: scope, UUID: h.options.UUID, Now: h.options.Now, Random: func() float64 { return 0 }, TLSState: h.options.TLSState, ObserveRequest: h.options.ObserveRequest, ObserveFetch: h.options.ObserveFetch,
		NativeAccountUUID: h.nativeRequestAccountMetadata(scope), NativeRetryEnvironment: h.nativeRetryPolicyEnvironment(), NativeParentClientState: parentClient,
		Sleep: func(ctx context.Context, millis float64) error {
			_, err := scope.AwaitBytes(cooperative.RetryTimer, func(wait context.Context) ([]byte, error) {
				timer := time.NewTimer(time.Duration(millis * float64(time.Millisecond)))
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return nil, context.Cause(ctx)
				case <-wait.Done():
					return nil, context.Cause(wait)
				case <-timer.C:
					return nil, nil
				}
			})
			return err
		},
		NativeOpenIO: providernative.NewPooledFactory(h.options.Owner, scope, h.options.ObserveNativeFetch, h.options.NativeHTTPPool),
		BodyIOAwait: func(kind provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
			if kind != provider.OwnerBodyRead && kind != provider.OwnerErrorBody {
				return nil, cooperative.ErrRestricted
			}
			return scope.AwaitBytes(cooperative.StreamNext, work)
		},
	}
}
