package modelyield

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/apipresentation"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

func runNativeNonstreamFallback(ctx context.Context, input requestretry.Input, deps Dependencies, requestDeps requestretry.Dependencies, domain apipresentation.Domain, factory *messages.Factory, history []protocol.JSONObject, requestID *string, cause string, original error, observe func(json.RawMessage) error) (Outcome, error) {
	control := streamRetryMessage(deps.Request.Now, deps.Request.UUID)
	control.Set("cause", streamRetryString(cause))
	control.Set("content", streamRetryString("Streaming request failed ("+strings.ReplaceAll(cause, "_", " ")+"); retrying in non-streaming mode"))
	action, err := deps.Emit(Yield{Control: &control})
	if action == requestretry.Stop && err == nil {
		return Outcome{Reason: "consumer_stopped"}, nil
	}
	if err = actionError(action, err); err != nil {
		return Outcome{}, err
	}
	// Pre-query callbacks belong to the outer attempt, not this second request.
	requestDeps.OnPrepared = nil
	requestDeps.OnPreparation = nil
	requestDeps.OnParamsPrepared = nil
	// MAIN's separate 404 creation recovery does not pass apiAttemptBudget to
	// executeNonStreamingRequest. Preserve that frozen branch explicitly; the
	// ordinary stream-error fallback still shares the caller's remaining budget.
	if cause == "404_stream_creation" {
		requestDeps.APIAttemptBudget = nil
	}
	initial529 := float64(0)
	var sdk *provider.SDKStreamError
	if errors.As(original, &sdk) && sdk.Status != nil && *sdk.Status == 529 {
		initial529 = 1
	}
	response, err := requestretry.CreateNativeFallback(ctx, input, requestDeps, initial529)
	if provider.IsOwnerWaitFailure(err) {
		return Outcome{}, err
	}
	if requestretry.IsCallerAbort(err) || ctx.Err() != nil {
		return Outcome{Reason: "caller_aborted"}, nil
	}
	if errors.Is(err, requestretry.ErrConsumerStopped) {
		return Outcome{Reason: "consumer_stopped"}, nil
	}
	var envelope *streamhistory.Envelope
	if err != nil {
		failure := err
		var selected bool
		envelope, selected, err = apipresentation.NativeMediaFailure(failure, domain, factory, mediaPresentationOptions(input, history))
		if err != nil {
			return Outcome{}, err
		}
		if !selected {
			envelope, err = apipresentation.NativeCreationFailure(failure, domain, factory, mediaPresentationOptions(input, history))
		}
		if err != nil {
			return Outcome{}, err
		}
	} else {
		envelope, err = streamhistory.NativeFallbackEnvelope(response.Raw, requestID, deps.Request.UUID, deps.Request.Now)
		if err != nil {
			return Outcome{}, err
		}
		if observe != nil {
			raw, e := envelope.MessageJSON()
			if e != nil {
				return Outcome{}, e
			}
			if err = observe(raw); err != nil {
				return Outcome{}, err
			}
		}
	}
	action, err = deps.Emit(Yield{Update: &streamhistory.Update{Envelope: envelope}})
	if action == requestretry.Stop && err == nil {
		return Outcome{Reason: "consumer_stopped", Terminal: envelope.APIError}, nil
	}
	if err = actionError(action, err); err != nil {
		return Outcome{}, err
	}
	return Outcome{Reason: "returned", Terminal: envelope.APIError}, nil
}
