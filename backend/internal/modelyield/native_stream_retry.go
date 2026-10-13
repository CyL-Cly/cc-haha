package modelyield

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/apipresentation"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

type transientStreamError struct {
	original     error
	buffered     []streamhistory.Update
	mediaHistory []protocol.JSONObject
	kind         string
}

func (e *transientStreamError) Error() string { return e.original.Error() }
func (e *transientStreamError) Unwrap() error { return e.original }

func nativeTransientStreamRetry(stream *provider.RawMessageStream, failure error, state streamhistory.AttemptState, policy *StreamErrorPolicy) (string, error) {
	if state.CommittedOutput || state.ServerToolUseStarted {
		return "", nil
	}
	var watchdog *streamhistory.WatchdogTimeoutError
	if errors.As(failure, &watchdog) {
		if watchdog.SafeToRetry() {
			return "watchdog", nil
		}
		return "", nil
	}
	if transport, err := retrypolicy.IsRetryableStreamTransportError(failure); transport || err != nil {
		if err != nil {
			return "", err
		}
		return "transport", nil
	}
	var early *streamhistory.StreamEndedEarlyError
	proxyTransport := false
	var api *provider.SDKStreamError
	if errors.As(failure, &api) && api.Status == nil {
		var event struct{ Error struct{ Type string } }
		if json.Unmarshal(api.Payload, &event) == nil {
			proxyTransport = event.Error.Type == "stream_error" || event.Error.Type == "stream_truncated"
		}
	}
	if errors.As(failure, &early) || proxyTransport {
		fallbackAvailable := !policy.disableFallback && !state.CrossedSideEffectBoundary
		if fallbackAvailable && (early == nil || early.Reason != "incomplete") {
			return "", nil
		}
		return "transport", nil
	}
	if state.CompletedMessages != 0 && !providerproxy.CanRetryOpenAICodexBufferedStream(stream.Headers.Get(providerproxy.OpenAICodexStreamMarkerHeader), false) {
		return "", nil
	}
	if retry, err := retrypolicy.IsRetryableStreamAPIError(failure); retry || err != nil {
		if err != nil {
			return "", err
		}
		return "transient", nil
	}
	return "", nil
}

func streamRetryMaximum(policy *StreamErrorPolicy, kind string, env map[string]string) float64 {
	if kind != "transport" {
		return float64(policy.maxTransientRetries)
	}
	max := retrypolicy.DefaultMaximum(env["CLAUDE_CODE_MAX_RETRIES"])
	if math.IsNaN(max) || math.IsInf(max, 0) || max < 0 {
		max = 10
	}
	if value, ok := policy.original["CLAUDE_STREAM_TRANSIENT_RETRY_MAX"]; ok {
		// A valid override caps the creation budget; an invalid one does not.
		if prefix := streamRetryOverride(value); prefix != nil {
			max = math.Min(max, *prefix)
		}
	}
	return max
}

// Each retry is a new queryModel attempt, on the same caller owner, history,
// context and turn-state. The failed attempt finishes its resources/accounting
// before the recovery signal is yielded; only a later consumer demand resends.
func runStreamAttempts(ctx context.Context, input requestretry.Input, deps Dependencies, owner *cooperative.Owner, scope *cooperative.Scope, begin func() (*producerLifecycle, error)) (Outcome, error) {
	for number := 0; ; number++ {
		life, err := begin()
		if err != nil {
			return Outcome{}, err
		}
		outcome, err := runOwnedLifecycle(ctx, input, deps, owner, scope, life)
		var transient *transientStreamError
		if !errors.As(err, &transient) {
			return outcome, err
		}
		if deps.StreamPolicy == nil || !deps.StreamPolicy.native || input.Context.Host.Profile != requestcontext.NativeDirectRelayProfile {
			return Outcome{}, unsupported("native transient stream retry admission")
		}
		maximum := streamRetryMaximum(deps.StreamPolicy, transient.kind, input.Context.Host.Env)
		if float64(number) >= maximum || deps.Request.APIAttemptBudget.Remaining() <= 0 {
			for _, buffered := range transient.buffered {
				action, failure := deps.Emit(Yield{Update: &buffered})
				if failure != nil {
					return Outcome{}, failure
				}
				if action == requestretry.Stop {
					return Outcome{Reason: "consumer_stopped"}, nil
				}
				if failure = actionError(action, nil); failure != nil {
					return Outcome{}, failure
				}
			}
			domain, failure := apipresentation.NativeDomainFromOriginalHost(input.Context.Host, input.ResolvedFastMode)
			if failure != nil {
				return Outcome{}, failure
			}
			factory, failure := messages.NewFactory(deps.Request.UUID, deps.Request.Now)
			if failure != nil {
				return Outcome{}, failure
			}
			var early *streamhistory.StreamEndedEarlyError
			if errors.As(transient.original, &early) {
				early.Retries = number
			}
			terminal, failure := nativeMediaStreamTerminal(transient.original, domain, factory, input, transient.mediaHistory)
			if failure != nil {
				return Outcome{}, failure
			}
			update := streamhistory.Update{Envelope: terminal}
			action, failure := deps.Emit(Yield{Update: &update})
			if failure != nil {
				return Outcome{}, failure
			}
			if action == requestretry.Stop {
				return Outcome{Reason: "consumer_stopped", Terminal: true}, nil
			}
			return Outcome{Reason: "returned", Terminal: true}, actionError(action, nil)
		}
		control := streamRetryMessage(deps.Request.Now, deps.Request.UUID)
		action, failure := deps.Emit(Yield{Control: &control})
		if failure != nil {
			return Outcome{}, failure
		}
		if action == requestretry.Stop {
			return Outcome{Reason: "consumer_stopped"}, nil
		}
		if failure = actionError(action, nil); failure != nil {
			return Outcome{}, failure
		}
		delay := retrypolicy.Delay(float64(number+1), nil, 32000, deps.Request.Random)
		var sdk *provider.SDKStreamError
		if !errors.As(transient.original, &sdk) {
			sdk = &provider.SDKStreamError{Class: "APIConnectionError", Kind: "request_connection", Name: "Error", Message: transient.original.Error(), Type: json.RawMessage(`null`), Cause: transient.original}
		}
		notice := retrypolicy.RetryNotice{Type: "system", Subtype: "api_error", Level: "error", Error: sdk, Cause: sdk.Cause, RetryInMillis: delay, RetryAttempt: float64(number + 1), MaxRetries: maximum, Timestamp: deps.Request.Now().UTC().Format("2006-01-02T15:04:05.000Z"), UUID: deps.Request.UUID()}
		action, failure = deps.Emit(Yield{Notice: &notice})
		if action == requestretry.Stop && failure == nil {
			return Outcome{Reason: "consumer_stopped"}, nil
		}
		if failure = actionError(action, failure); failure != nil {
			return Outcome{}, failure
		}
		if failure = deps.Request.Sleep(ctx, delay); failure != nil {
			if ctx.Err() != nil {
				return Outcome{Reason: "caller_aborted"}, nil
			}
			return Outcome{}, failure
		}
		if ctx.Err() != nil {
			return Outcome{Reason: "caller_aborted"}, nil
		}
	}
}

func streamRetryMessage(now func() time.Time, uuid func() string) protocol.JSONObject {
	timestamp := now().UTC().Format("2006-01-02T15:04:05.000Z")
	return protocol.NewJSONObject(
		protocol.JSONField{Name: "type", Value: streamRetryString("system")},
		protocol.JSONField{Name: "subtype", Value: streamRetryString("streaming_fallback")},
		protocol.JSONField{Name: "level", Value: streamRetryString("info")},
		protocol.JSONField{Name: "content", Value: streamRetryString("Provider stream was interrupted before any tool ran; retrying safely")},
		protocol.JSONField{Name: "cause", Value: streamRetryString("stream_retry")},
		protocol.JSONField{Name: "timestamp", Value: streamRetryString(timestamp)},
		protocol.JSONField{Name: "uuid", Value: streamRetryString(uuid())},
	)
}

func streamRetryString(value string) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}
