package modelyield

import (
	"context"
	"encoding/json"
	"errors"
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
}

func (e *transientStreamError) Error() string { return e.original.Error() }
func (e *transientStreamError) Unwrap() error { return e.original }

func nativeTransientStreamRetry(stream *provider.RawMessageStream, failure error, state streamhistory.AttemptState) (bool, error) {
	if state.CrossedSideEffectBoundary {
		return false, nil
	}
	if transport, err := retrypolicy.IsRetryableStreamTransportError(failure); transport || err != nil {
		return transport, err
	}
	if state.CompletedMessages != 0 && !providerproxy.CanRetryOpenAICodexBufferedStream(stream.Headers.Get(providerproxy.OpenAICodexStreamMarkerHeader), state.CrossedSideEffectBoundary) {
		return false, nil
	}
	return retrypolicy.IsRetryableStreamAPIError(failure)
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
		if number >= deps.StreamPolicy.maxTransientRetries {
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
	}
}

func streamRetryMessage(now func() time.Time, uuid func() string) protocol.JSONObject {
	timestamp := now().UTC().Format("2006-01-02T15:04:05.000Z")
	return protocol.NewJSONObject(
		protocol.JSONField{Name: "type", Value: streamRetryString("system")},
		protocol.JSONField{Name: "subtype", Value: streamRetryString("streaming_fallback")},
		protocol.JSONField{Name: "level", Value: streamRetryString("info")},
		protocol.JSONField{Name: "content", Value: streamRetryString("Provider stream stalled before a tool side effect; retrying safely")},
		protocol.JSONField{Name: "cause", Value: streamRetryString("stream_retry")},
		protocol.JSONField{Name: "timestamp", Value: streamRetryString(timestamp)},
		protocol.JSONField{Name: "uuid", Value: streamRetryString(uuid())},
	)
}

func streamRetryString(value string) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}
