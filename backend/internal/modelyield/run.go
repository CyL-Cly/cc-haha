// Package modelyield connects one physical model creation operation and its
// single-response consumer. Session history, hooks, cost/activity logging and
// outer-query recovery remain explicit, separately migrated host boundaries.
package modelyield

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"io"

	"github.com/NanmiCoder/cc-haha/backend/internal/apipresentation"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerlogging"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionactivity"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// Yield contains either a retry notice or a live stream/assistant update. The
// synchronous consumer suspends creation before the next wait/request, or
// suspends terminal completion before returning to the outer query.
type Yield struct {
	Notice  *retrypolicy.RetryNotice
	Update  *streamhistory.Update
	Control *protocol.JSONObject
}

type Dependencies struct {
	OnNativeQuota   func(*cooperative.Scope, *provider.BunHeaders, error) error
	Activity        *sessionactivity.Tracker
	Request         requestretry.Dependencies
	Session         *modelaccounting.Session
	AgentContext    *modelaccounting.AgentContextBinding
	SuccessDuration *modelaccounting.SuccessDuration
	StreamClock     *ProducerClock
	StreamPolicy    *StreamErrorPolicy
	Emit            func(Yield) (requestretry.NoticeAction, error)
}

type Outcome struct {
	// returned includes normal return after a terminal assistant API error. It
	// is not evidence of a successful model response or outer-query completion.
	Reason   string
	Terminal bool
}

type CapabilityError struct{ Boundary string }

func (failure *CapabilityError) Error() string {
	return "model yield capability is not migrated: " + failure.Boundary
}

func unsupported(boundary string) error { return &CapabilityError{Boundary: boundary} }

func actionError(action requestretry.NoticeAction, err error) error {
	if err != nil {
		return err
	}
	switch action {
	case requestretry.Resume:
		return nil
	case requestretry.Stop:
		return requestretry.ErrConsumerStopped
	default:
		return unsupported("consumer action")
	}
}

// Run takes original history/options/environment. Request preparation and
// every retry are owned by the shared opener; a successful stream is consumed
// once and never resubmitted here. No Source-rendered error text is accepted.
func Run(ctx context.Context, input requestretry.Input, deps Dependencies) (Outcome, error) {
	if err := requestretry.BindNativeVertexHost(&input, deps.Request); err != nil {
		return Outcome{}, err
	}
	if err := requestretry.BindNativeBedrockHost(&input, deps.Request); err != nil {
		return Outcome{}, err
	}
	if input.Context.Host.NativeCodexOAuth == nil {
		input.Context.Host.NativeCodexOAuth = deps.Request.NativeCodexOAuth
	}
	if deps.Activity != nil && (deps.Request.StreamOwner == nil || !deps.Activity.Owns(deps.Request.StreamOwner)) {
		return Outcome{}, unsupported("activity requires original same-owner producer")
	}
	if (deps.Session == nil) != (deps.AgentContext == nil) || (deps.Session != nil && deps.Request.StreamOwner == nil) {
		return Outcome{}, unsupported("accounting requires original same-owner producer and Agent context")
	}
	if deps.Request.StreamOwner != nil || deps.Request.StreamScope != nil {
		if !deps.Request.StreamScope.BelongsTo(deps.Request.StreamOwner) {
			return Outcome{}, unsupported("fresh same-owner stream demand admission")
		}
		return runOwned(ctx, input, deps, deps.Request.StreamOwner, deps.Request.StreamScope)
	}
	if deps.Request.NativeOpenIO != nil || deps.Request.OpenIOAwait != nil || deps.Request.BodyIOAwait != nil {
		return Outcome{}, unsupported("owner bridge requires current model Scope")
	}
	// Component callers still use the identical demand implementation. An owner
	// lifetime outlives model cancellation so Abort/finally can complete.
	owner := cooperative.New()
	defer owner.Close()
	var result Outcome
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var failure error
		result, failure = runOwned(ctx, input, deps, owner, scope)
		return failure
	})
	return result, err
}

type producerLifecycle struct {
	opened   func(*provider.OwnedMessageStream)
	feed     func([]byte) error
	finally  func(bool, *string) error
	fallback func(json.RawMessage) error
}

func runOwned(ctx context.Context, input requestretry.Input, deps Dependencies, owner *cooperative.Owner, scope *cooperative.Scope) (Outcome, error) {
	return runStreamAttempts(ctx, input, deps, owner, scope, func() (*producerLifecycle, error) {
		return beginProducerLifecycle(input, deps, owner, scope, nil)
	})
}

func beginProducerLifecycle(input requestretry.Input, deps Dependencies, owner *cooperative.Owner, scope *cooperative.Scope, opened func(*provider.OwnedMessageStream)) (*producerLifecycle, error) {
	var life *producerLifecycle
	if deps.Session != nil {
		if !deps.Session.Owns(owner) || !deps.AgentContext.Owns(owner) {
			return nil, unsupported("same-owner producer accounting/context")
		}
		attempt, err := deps.Session.BeginRequest(scope, input, deps.AgentContext)
		if err != nil {
			return nil, err
		}
		if opened == nil {
			opened = func(*provider.OwnedMessageStream) {}
		}
		life = &producerLifecycle{opened: opened, feed: func(raw []byte) error { return attempt.Observe(scope, raw) }, finally: func(normal bool, id *string) error { return attempt.Finish(scope, normal, id) }, fallback: func(raw json.RawMessage) error { return attempt.ObserveFallback(scope, raw) }}
	}
	return life, nil
}
func runOwnedLifecycle(ctx context.Context, input requestretry.Input, deps Dependencies, owner *cooperative.Owner, scope *cooperative.Scope, life *producerLifecycle) (outcome Outcome, resultErr error) {
	if ctx == nil || deps.Emit == nil || deps.Request.Notice != nil {
		return Outcome{}, unsupported("caller context and single model-yield consumer")
	}
	if deps.Activity != nil && !deps.Activity.Owns(owner) {
		return Outcome{}, unsupported("same-owner process activity")
	}
	domain, err := apipresentation.DomainFromOriginalEnvAndFastMode(input.Context.Host.Env, input.ResolvedFastMode)
	if input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile {
		domain, err = apipresentation.NativeDomainFromOriginalHost(input.Context.Host, input.ResolvedFastMode)
	}
	if err != nil {
		return Outcome{}, err
	}
	factory, err := messages.NewFactory(deps.Request.UUID, deps.Request.Now)
	if err != nil {
		return Outcome{}, err
	}
	fields := producerFields{clock: deps.StreamClock}
	if fields.clock != nil && fields.clock.NowMillis == nil {
		return Outcome{}, unsupported("original producer Date.now")
	}
	var duration *modelaccounting.DurationAttempt
	if deps.SuccessDuration != nil {
		if !deps.Session.Owns(owner) {
			return Outcome{}, unsupported("success duration requires original Session")
		}
		if deps.SuccessDuration.NowMillis == nil || deps.SuccessDuration.GetPermission == nil {
			return Outcome{}, unsupported("original success clock/permission Promise")
		}
	}
	if deps.StreamClock != nil && deps.SuccessDuration != nil {
		fields.clock = &ProducerClock{NowMillis: deps.SuccessDuration.NowMillis}
	}
	requestDeps := deps.Request
	var mediaHistory []protocol.JSONObject
	if input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile {
		originalPrepared := requestDeps.OnPrepared
		requestDeps.OnPrepared = func(history []protocol.JSONObject) error {
			if originalPrepared != nil {
				if err := originalPrepared(history); err != nil {
					return err
				}
			}
			var err error
			mediaHistory, err = captureMediaHistory(history)
			return err
		}
	}
	var durationFault error
	if deps.SuccessDuration != nil {
		originalPrepared := requestDeps.OnPrepared
		requestDeps.OnPrepared = func(history []protocol.JSONObject) error {
			if originalPrepared != nil {
				if failure := originalPrepared(history); failure != nil {
					durationFault = failure
					return failure
				}
			}
			duration, durationFault = deps.Session.BeginDuration(scope, *deps.SuccessDuration)
			if durationFault != nil {
				return durationFault
			}
			if input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile {
				return nil
			}
			logInput, failure := providerlogging.FromOriginal(input.Context, history)
			if failure != nil {
				durationFault = failure
				return failure
			}
			durationFault = duration.RegisterQuery(scope, logInput)
			return durationFault
		}
		if input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile {
			originalParamsPrepared := requestDeps.OnParamsPrepared
			requestDeps.OnParamsPrepared = func(result *requestcontext.Result) error {
				if originalParamsPrepared != nil {
					if failure := originalParamsPrepared(result); failure != nil {
						durationFault = failure
						return failure
					}
				}
				logInput, failure := providerlogging.FromParams(input.Context, result, input.ResolvedFastMode)
				if failure != nil {
					durationFault = failure
					return failure
				}
				durationFault = duration.RegisterQuery(scope, logInput)
				return durationFault
			}
		}
		originalAttempt := requestDeps.OnAttempt
		requestDeps.OnAttempt = func(number float64) {
			if durationFault == nil {
				durationFault = duration.StartAttempt(scope)
			}
			if durationFault == nil && fields.clock != nil {
				fields.start, durationFault = duration.AttemptStartMillis(scope)
				fields.first = nil
			}
			if originalAttempt != nil {
				originalAttempt(number)
			}
		}
	}
	if deps.SuccessDuration == nil && fields.clock != nil {
		originalAttempt := requestDeps.OnAttempt
		requestDeps.OnAttempt = func(number float64) {
			fields.start, durationFault = fields.now()
			fields.first = nil
			if originalAttempt != nil {
				originalAttempt(number)
			}
		}
	}
	// Registered before stream.Close/finally so it runs after both. This is not
	// a wait: the actual permission reaction remains queued or pending afterwards.
	var normalCompletion bool
	defer func() {
		if normalCompletion && resultErr == nil && duration != nil {
			if durationFault != nil {
				resultErr = durationFault
				return
			}
			resultErr = duration.RegisterSuccess(scope)
		}
	}()
	var consumerFailure error
	consumerStopped := false
	requestDeps.Notice = func(_ context.Context, notice retrypolicy.RetryNotice) (requestretry.NoticeAction, error) {
		action, failure := deps.Emit(Yield{Notice: &notice})
		consumerStopped = failure == nil && action == requestretry.Stop
		consumerFailure = actionError(action, failure)
		return action, consumerFailure
	}
	// This marker is after Prepare and the real pre-query duration/getter body.
	// A creation error after it must execute stop, even if no start occurred.
	creationEntered := false
	originalPrepared := requestDeps.OnPrepared
	requestDeps.OnPrepared = func(history []protocol.JSONObject) error {
		if originalPrepared != nil {
			if err := originalPrepared(history); err != nil {
				return err
			}
		}
		if input.Context.Host.Profile != requestcontext.NativeDirectRelayProfile {
			creationEntered = true
		}
		return nil
	}
	if input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile {
		originalParamsPrepared := requestDeps.OnParamsPrepared
		requestDeps.OnParamsPrepared = func(result *requestcontext.Result) error {
			if originalParamsPrepared != nil {
				if err := originalParamsPrepared(result); err != nil {
					return err
				}
			}
			creationEntered = true
			return nil
		}
	}
	var stream *provider.RawMessageStream
	var owned *provider.OwnedMessageStream
	lifeOpened := false
	defer func() {
		if creationEntered && deps.Activity != nil {
			if failure := deps.Activity.Stop(scope, sessionactivity.APICall); resultErr == nil && failure != nil {
				resultErr = failure
			}
		}
		// Source releases resources before the unique producer accounting finally.
		if owned != nil {
			_ = owned.AbortSDK(scope)
		}
		if stream != nil {
			_ = stream.Close()
		}
		if lifeOpened && life != nil {
			var originalID *string
			if stream != nil {
				originalID = stream.RequestID
			}
			if failure := life.finally(normalCompletion, originalID); resultErr == nil && failure != nil {
				resultErr = failure
			}
		}
	}()
	stream, err = requestretry.Open(ctx, input, requestDeps)
	// Source pre-query getter access is outside creation's API error catch.
	if durationFault != nil {
		return Outcome{}, durationFault
	}
	if consumerFailure != nil {
		if consumerStopped {
			return Outcome{Reason: "consumer_stopped"}, nil
		}
		return Outcome{}, consumerFailure
	}
	// Native header/error-body completion can fail administratively before a
	// stream exists. Keep any retry wrapper and consumer ownership; never turn
	// that host failure into a synthetic API terminal or caller-abort result.
	if provider.IsOwnerWaitFailure(err) {
		return Outcome{}, err
	}
	if requestretry.IsCallerAbort(err) {
		return Outcome{Reason: "caller_aborted"}, nil
	}
	if err != nil {
		var creation *provider.SDKStreamError
		if input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile && errors.As(err, &creation) && creation.Status != nil && *creation.Status == 404 {
			rejected, failure := retrypolicy.HasPolicyRejection(err)
			if failure != nil {
				return Outcome{}, failure
			}
			if !rejected {
				outcome, failure := runNativeNonstreamFallback(ctx, input, deps, requestDeps, domain, factory, mediaHistory, nil, "404_stream_creation", err, func(raw json.RawMessage) error {
					if life != nil {
						lifeOpened = true
						return life.fallback(raw)
					}
					return nil
				})
				normalCompletion = failure == nil && outcome.Reason == "returned" && !outcome.Terminal
				return outcome, failure
			}
		}
		if deps.OnNativeQuota != nil {
			if e := deps.OnNativeQuota(scope, nil, err); e != nil {
				return Outcome{}, e
			}
		}
		var terminal *streamhistory.Envelope
		var failure error
		matched := false
		if input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile {
			terminal, matched, failure = apipresentation.NativeMediaFailure(err, domain, factory, mediaPresentationOptions(input, mediaHistory))
		}
		if failure != nil {
			return Outcome{}, failure
		}
		if !matched {
			if _, exhausted := err.(*retrypolicy.CannotRetryError); !exhausted {
				// Capabilities, preparation and administrative dependencies remain
				// their actual errors. Only represented media faults were selected.
				return Outcome{}, err
			}
			// Allocate only after the resumed final retry notice, using this
			// attempt's shared Factory and captured actual prepared history.
			if input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile {
				terminal, failure = apipresentation.NativeCreationFailure(err, domain, factory, mediaPresentationOptions(input, mediaHistory))
			} else {
				terminal, failure = apipresentation.CreationFailure(err, domain, factory)
			}
		}
		if failure != nil {
			return Outcome{}, failure
		}
		update := streamhistory.Update{Envelope: terminal}
		action, failure := deps.Emit(Yield{Update: &update})
		consumerStopped = failure == nil && action == requestretry.Stop
		failure = actionError(action, failure)
		if consumerStopped {
			return Outcome{Reason: "consumer_stopped", Terminal: true}, nil
		}
		if failure != nil {
			return Outcome{}, failure
		}
		return Outcome{Reason: "returned", Terminal: true}, nil
	}
	if durationFault != nil {
		return Outcome{}, durationFault
	}
	owned, err = provider.NewOwnedMessageStream(scope, owner, stream)
	if err != nil {
		return Outcome{}, err
	}
	if life != nil {
		life.opened(owned)
		lifeOpened = true
	}
	if deps.Activity != nil {
		if failure := deps.Activity.Start(scope, sessionactivity.APICall); failure != nil {
			return Outcome{}, failure
		}
	}
	var terminalAPIError, finishing bool
	controller, err := streamhistory.New(streamhistory.Options{
		UUID: deps.Request.UUID, Now: deps.Request.Now, RequestID: stream.RequestID,
		RefusalProfile: &streamhistory.RefusalProfile{Model: input.Context.Options.Model, NonInteractive: input.Context.Options.IsNonInteractiveSession},
	})
	if err != nil {
		return Outcome{}, err
	}
	var watchdog *nativeStreamWatchdog
	if input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile && deps.StreamPolicy != nil && deps.StreamPolicy.native {
		env := deps.StreamPolicy.OriginalEnvironment()
		if env["API_TIMEOUT_MS"] == "" {
			env["API_TIMEOUT_MS"] = input.Context.Host.Env["API_TIMEOUT_MS"]
		}
		watchdog, err = newNativeStreamWatchdog(scope, owned, stream, env)
		if err != nil {
			return Outcome{}, err
		}
		defer watchdog.close()
	}
	emit := func(update streamhistory.Update) error {
		if finishing && update.Envelope != nil && update.Envelope.APIError {
			terminalAPIError = true
		}
		action, failure := deps.Emit(Yield{Update: &update})
		consumerStopped = failure == nil && action == requestretry.Stop
		consumerFailure = actionError(action, failure)
		return consumerFailure
	}
	for {
		pending, failure := owned.Iterator.Next(scope)
		failure = provider.StreamDemandFailure(failure, failure != nil)
		var part []byte
		if failure == nil {
			result, err, administrative := pending.AwaitClassified(scope)
			failure = provider.StreamDemandFailure(err, administrative)
			if failure == nil {
				if result.Done {
					failure = io.EOF
				} else {
					part, failure = result.Value.ReducerJSON()
				}
			}
		}
		if errors.Is(failure, cooperative.ErrClosed) || errors.Is(failure, cooperative.ErrRestricted) {
			return Outcome{}, failure
		}
		if failure == nil {
			if failure = watchdog.record(part); failure != nil {
				return Outcome{}, failure
			}
			if life != nil {
				if failure = life.feed(part); failure != nil {
					return Outcome{}, failure
				}
			}
			timing, timingErr := fields.observe(part)
			if timingErr != nil {
				return Outcome{}, timingErr
			}
			failure = scope.Frame(cooperative.FrameFeed, func() error { return controller.Feed(part, timing, emit) })
			if failure == nil {
				continue
			}
		}
		var thrown *streamhistory.YieldThrow
		if consumerFailure != nil {
			// Source for-await closes its actual SDK iterator on abrupt outer
			// return/consumer throw before queryModel's resource finally. The
			// queued entry follows that demand and awaits its real close.
			if life != nil {
				closing, closeErr := owned.Iterator.ReturnUndefined(scope)
				if closeErr == nil {
					_, e, administrative := closing.AwaitClassified(scope)
					closeErr = provider.StreamDemandFailure(e, administrative)
				}
				if consumerStopped && closeErr != nil {
					return Outcome{}, closeErr
				}
			}
			if consumerStopped {
				return Outcome{Reason: "consumer_stopped"}, nil
			}
			if errors.As(consumerFailure, &thrown) && thrown.Original != nil {
				failure = thrown.Original
				consumerFailure = nil
			} else {
				return Outcome{}, consumerFailure
			}
		}
		// An owner bridge failure is administrative, not a caller signal or
		// native transport fault. Preserve it without a partial-text commit.
		if provider.IsOwnerWaitFailure(failure) {
			return Outcome{}, failure
		}
		// The original caller signal wins over an incidental transport failure,
		// after preserving consumer ownership. Flush through the same live
		// assembler so visible text survives and pending tools stay uncommitted.
		if ctx.Err() != nil || stream.AbortCause() != nil && (watchdog == nil || watchdog.failure == nil) {
			if failure = scope.Frame(cooperative.FrameAbort, func() error { return controller.Abort(emit) }); failure != nil {
				if consumerStopped {
					return Outcome{Reason: "consumer_stopped"}, nil
				}
				return Outcome{}, failure
			}
			return Outcome{Reason: "caller_aborted"}, nil
		}
		if watchdog != nil && watchdog.failure != nil {
			failure = watchdog.failure
		}
		if errors.Is(failure, io.EOF) && input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile {
			now, e := fields.now()
			if e != nil {
				return Outcome{}, e
			}
			if early := controller.EndOfStream(now - fields.start); early != nil {
				failure = early
			}
		}
		if !errors.Is(failure, io.EOF) {
			nativePolicy := input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile && deps.StreamPolicy != nil && deps.StreamPolicy.native
			if deps.StreamPolicy == nil || !deps.StreamPolicy.disabled && !nativePolicy {
				return Outcome{}, unsupported("partial-stream error, retry and commit buffer")
			}
			state := controller.AttemptState()
			rejected, policyError := retrypolicy.HasPolicyRejection(failure)
			if policyError != nil {
				return Outcome{}, policyError
			} else if rejected && !nativePolicy {
				return Outcome{}, unsupported("stream policy rejection presentation")
			}
			if nativePolicy && !rejected {
				kind, e := nativeTransientStreamRetry(stream, failure, state, deps.StreamPolicy)
				if e != nil {
					return Outcome{}, e
				}
				if kind != "" {
					// Source claude.ts disables the next client's keepalive before
					// flushing this failed attempt, only if no message_start arrived
					// and the original fault is a socket transport failure.
					if !state.PartialMessage {
						transport, e := retrypolicy.IsRetryableStreamTransportError(failure)
						if e != nil {
							return Outcome{}, e
						}
						if disable := deps.Request.NativeKeepAliveDisable(); transport && disable != nil {
							if e := disable(); e != nil {
								return Outcome{}, e
							}
						}
					}
					buffered := []streamhistory.Update{}
					if e := scope.Frame(cooperative.FrameFinish, func() error {
						return controller.FlushProducerErrorWithoutToolUse(func(update streamhistory.Update) error {
							buffered = append(buffered, update)
							return nil
						})
					}); e != nil {
						return Outcome{}, e
					}
					return Outcome{}, &transientStreamError{original: failure, buffered: buffered, mediaHistory: mediaHistory, kind: kind}
				}
			}
			if retry, e := retrypolicy.IsRetryableStreamTransportError(failure); e != nil {
				return Outcome{}, e
			} else if retry && (!nativePolicy || deps.StreamPolicy.maxTransientRetries > 0 && !state.CrossedSideEffectBoundary) {
				return Outcome{}, unsupported("actual stream transport retry")
			}
			if retry, e := retrypolicy.IsRetryableStreamAPIError(failure); e != nil {
				return Outcome{}, e
			} else if retry && (!nativePolicy || deps.StreamPolicy.maxTransientRetries > 0 && state.CompletedMessages == 0 && !state.CrossedSideEffectBoundary) {
				return Outcome{}, unsupported("actual transient stream retry")
			}
			if nativePolicy && !rejected && !deps.StreamPolicy.disableFallback && !state.CrossedSideEffectBoundary {
				var early *streamhistory.StreamEndedEarlyError
				incomplete := errors.As(failure, &early) && early.Reason == "incomplete"
				if !state.CommittedOutput && !state.ServerToolUseStarted && !incomplete {
					cause := "stream_error"
					if watchdog != nil && watchdog.failure != nil {
						cause = "watchdog"
					}
					outcome, err := runNativeNonstreamFallback(ctx, input, deps, requestDeps, domain, factory, mediaHistory, stream.RequestID, cause, failure, func(raw json.RawMessage) error {
						if life != nil {
							return life.fallback(raw)
						}
						return nil
					})
					normalCompletion = err == nil && outcome.Reason == "returned" && !outcome.Terminal
					return outcome, err
				}
			}
			// Source outer catch drains before constructing the terminal. Its
			// UUID/Date allocation must remain after the resumed assistant yield.
			if e := scope.Frame(cooperative.FrameFinish, func() error {
				if nativePolicy && state.CrossedSideEffectBoundary {
					return controller.FlushProducerErrorWithoutToolUse(emit)
				}
				return controller.FlushProducerError(emit)
			}); e != nil {
				if consumerStopped {
					return Outcome{Reason: "consumer_stopped"}, nil
				}
				return Outcome{}, e
			}
			if deps.OnNativeQuota != nil {
				if e := deps.OnNativeQuota(scope, nil, failure); e != nil {
					return Outcome{}, e
				}
			}
			var terminal *streamhistory.Envelope
			var e error
			if nativePolicy {
				terminal, e = nativeMediaStreamTerminal(failure, domain, factory, input, mediaHistory)
			} else {
				terminal, e = apipresentation.StreamFailure(failure, domain, factory)
			}
			if e != nil {
				return Outcome{}, e
			}
			update := streamhistory.Update{Envelope: terminal}
			if e := emit(update); e != nil {
				if consumerStopped {
					return Outcome{Reason: "consumer_stopped", Terminal: true}, nil
				}
				return Outcome{}, e
			}
			return Outcome{Reason: "returned", Terminal: true}, nil
		}
		if deps.OnNativeQuota != nil {
			if e := deps.OnNativeQuota(scope, stream.OriginalHeaders, nil); e != nil {
				return Outcome{}, e
			}
		}
		finishing = true
		if failure = scope.Frame(cooperative.FrameFinish, func() error { return controller.Finish(nil, emit) }); failure != nil {
			if consumerStopped {
				return Outcome{Reason: "consumer_stopped"}, nil
			}
			return Outcome{}, failure
		}
		normalCompletion = !terminalAPIError
		return Outcome{Reason: "returned", Terminal: life != nil && terminalAPIError}, nil
	}
}
