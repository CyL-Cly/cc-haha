package modelyield

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionactivity"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// QueryValue carries a live producer yield or an original consumer return value.
// A return value is not a prepared response or a Source-observed yield.
type QueryValue struct {
	Yield       *Yield
	ReturnValue json.RawMessage
}
type QueryIterator = ownerasync.OwnerIterator[QueryValue]
type QueryResult = ownerasync.IteratorResult[QueryValue]
type QueryRequest = ownerasync.IteratorRequest[QueryValue]

// ResourceControl is the single production SDK controller, independent of the
// original caller signal. Every state operation requires the existing Owner.
type ResourceControl struct {
	owner  *cooperative.Owner
	stream *provider.OwnedMessageStream
}

func (c *ResourceControl) Cleanup(scope *cooperative.Scope) error {
	if c == nil || !scope.BelongsTo(c.owner) || c.stream == nil {
		return cooperative.ErrRestricted
	}
	return c.stream.AbortSDK(scope)
}
func (c *ResourceControl) State(scope *cooperative.Scope) (present, aborted bool, err error) {
	if c == nil || !scope.BelongsTo(c.owner) {
		return false, false, cooperative.ErrRestricted
	}
	if c.stream == nil {
		return false, false, nil
	}
	aborted, err = c.stream.SDKAborted(scope)
	return true, aborted, err
}

// QueuedDependencies starts the actual consumer on one Owner and receives its
// completion Promise. The consumer issues real Next/Return demands; it cannot
// invoke the model producer or submit a second prepared request.
type QueuedDependencies struct {
	OnNativeQuota   func(*cooperative.Scope, *provider.BunHeaders, error) error
	Activity        *sessionactivity.Tracker
	Request         requestretry.Dependencies
	Session         *modelaccounting.Session
	AgentContext    *modelaccounting.AgentContextBinding
	SuccessDuration *modelaccounting.SuccessDuration
	StreamClock     *ProducerClock
	StreamPolicy    *StreamErrorPolicy
	Start           func(*cooperative.Scope, *QueryIterator, *ResourceControl) (*ownerasync.OwnerFuture[struct{}], error)
}
type queuedBridge struct {
	owner              *cooperative.Owner
	producer, outer    *QueryIterator
	resume             *ownerasync.OwnerFuture[QueryRequest]
	started, returning bool
	consumerFailure    error
}

func newQueuedBridge(owner *cooperative.Owner) (*queuedBridge, error) {
	if owner == nil {
		return nil, cooperative.ErrRestricted
	}
	bridge := &queuedBridge{owner: owner}
	step := func(scope *cooperative.Scope, it *QueryIterator, request QueryRequest) error {
		if err := it.HoldProducer(scope); err != nil {
			return err
		}
		if !bridge.started {
			bridge.started = true
			return nil
		}
		if bridge.resume == nil {
			return cooperative.ErrRestricted
		}
		bridge.returning = request.Returning
		return bridge.resume.Resolve(scope, request)
	}
	var err error
	bridge.producer, err = ownerasync.NewOwnerIterator(owner, step)
	if err != nil {
		return nil, err
	}
	// Actual production delegation graph: queryModel -> withStreamRetry -> the
	// anonymous generator -> withStreamingVCR -> queryModelWithStreaming.
	// VCR stays disabled; native transient retries retain these consumer layers.
	inner := bridge.producer
	for _, layer := range []string{"withStreamRetry", "anonymous", "withStreamingVCR", "queryModelWithStreaming"} {
		delegate := inner
		forwardNormal := layer == "withStreamingVCR" || layer == "queryModelWithStreaming"
		inner, err = ownerasync.NewOwnerIterator(owner, func(scope *cooperative.Scope, it *QueryIterator, request QueryRequest) error {
			return it.Delegate(scope, delegate, request, func(next *cooperative.Scope, result QueryResult, request QueryRequest) error {
				if request.Returning {
					return it.CompleteReturn(next)
				}
				if forwardNormal {
					return it.CompleteDefined(next, result.Value, result.Defined)
				}
				return it.Complete(next)
			}, func(next *cooperative.Scope, failure error) error { return it.Fail(next, failure) })
		})
		if err != nil {
			return nil, err
		}
	}
	bridge.outer = inner
	return bridge, nil
}
func (b *queuedBridge) emit(scope *cooperative.Scope, value Yield) (requestretry.NoticeAction, error) {
	if !scope.BelongsTo(b.owner) {
		return 0, cooperative.ErrRestricted
	}
	if b.consumerFailure != nil {
		return 0, b.consumerFailure
	}
	b.resume, _ = ownerasync.NewOwnerFuture[QueryRequest](b.owner)
	if err := b.producer.Yield(scope, QueryValue{Yield: &value}, func(next *cooperative.Scope, it *QueryIterator, request QueryRequest) error {
		if err := it.HoldProducer(next); err != nil {
			return err
		}
		b.returning = request.Returning
		return b.resume.Resolve(next, request)
	}); err != nil {
		return 0, err
	}
	// Yield leaves the real Feed/Finish Frame on this stack. Only the actual later
	// Next or queued Return can resume it; no update list is collected in advance.
	if err := scope.AwaitPromise(b.resume.Done()); err != nil {
		return 0, err
	}
	request, err := b.resume.Wait(context.Background())
	if err != nil {
		return 0, err
	}
	if request.Returning {
		return requestretry.Stop, nil
	}
	if request.Throwing {
		return 0, &streamhistory.YieldThrow{Original: request.Failure}
	}
	return requestretry.Resume, nil
}
func (b *queuedBridge) finish(scope *cooperative.Scope, failure error) error {
	if b.returning && failure == nil {
		return b.producer.CompleteReturn(scope)
	}
	if failure != nil {
		return b.producer.Fail(scope, failure)
	}
	return b.producer.Complete(scope)
}

// RunQueued consumes one original request through the identical Run producer.
// It never creates an Owner, starts a business goroutine or uses compatibility
// Raw.Next. Shared session accounting runs at the producer's actual finally.
func RunQueued(ctx context.Context, input requestretry.Input, deps QueuedDependencies) (Outcome, error) {
	if input.Context.Host.NativeCodexOAuth == nil {
		input.Context.Host.NativeCodexOAuth = deps.Request.NativeCodexOAuth
	}
	owner, scope := deps.Request.StreamOwner, deps.Request.StreamScope
	if ctx == nil || !scope.BelongsTo(owner) || !deps.Session.Owns(owner) || !deps.AgentContext.Owns(owner) || deps.Start == nil || deps.Request.Notice != nil {
		return Outcome{}, unsupported("same-owner queued query consumer/session")
	}
	attempt, err := deps.Session.BeginRequest(scope, input, deps.AgentContext)
	if err != nil {
		return Outcome{}, err
	}
	bridge, err := newQueuedBridge(owner)
	if err != nil {
		return Outcome{}, err
	}
	control := &ResourceControl{owner: owner}
	completion, err := deps.Start(scope, bridge.outer, control)
	if err != nil {
		return Outcome{}, err
	}
	if completion == nil || !bridge.started {
		return Outcome{}, unsupported("consumer original first Next")
	}
	_, err = completion.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			bridge.consumerFailure = failure
			if bridge.resume != nil {
				return bridge.resume.Reject(next, failure)
			}
		}
		return nil
	})
	if err != nil {
		return Outcome{}, err
	}
	life := &producerLifecycle{opened: func(stream *provider.OwnedMessageStream) { control.stream = stream }, feed: func(raw []byte) error { return attempt.Observe(scope, raw) }, finally: func(normal bool, id *string) error { return attempt.Finish(scope, normal, id) }}
	producerDeps := Dependencies{OnNativeQuota: deps.OnNativeQuota, Activity: deps.Activity, Request: deps.Request, Session: deps.Session, AgentContext: deps.AgentContext, SuccessDuration: deps.SuccessDuration, StreamClock: deps.StreamClock, StreamPolicy: deps.StreamPolicy, Emit: func(value Yield) (requestretry.NoticeAction, error) { return bridge.emit(scope, value) }}
	firstAttempt := true
	outcome, err := runStreamAttempts(ctx, input, producerDeps, owner, scope, func() (*producerLifecycle, error) {
		if firstAttempt {
			firstAttempt = false
			return life, nil
		}
		return beginProducerLifecycle(input, producerDeps, owner, scope, func(stream *provider.OwnedMessageStream) { control.stream = stream })
	})
	if bridge.consumerFailure != nil {
		return outcome, bridge.consumerFailure
	}
	if failure := bridge.finish(scope, err); failure != nil {
		return outcome, failure
	}
	_, consumerErr := completion.Await(scope)
	if consumerErr != nil {
		return outcome, consumerErr
	}
	return outcome, err
}
