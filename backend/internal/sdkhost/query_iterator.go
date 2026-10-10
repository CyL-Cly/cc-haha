package sdkhost

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

// queryValue retains the original Go loop value or terminal. The terminal is
// returned through query(), then discarded by fileHistory's yield* expression.
// These are producer objects, never decoded Source observations.
type queryValue struct {
	Yield    queryloop.Yield
	Terminal *queryloop.Outcome
}

type queryIteratorBridge struct {
	owner                               *cooperative.Owner
	loop, query, history                *OwnerIterator[queryValue]
	next                                *OwnerFuture[IteratorResult[queryValue]]
	externalReturn                      *OwnerFuture[IteratorResult[queryValue]]
	agentReturnReceipt                  *OwnerFuture[struct{}]
	agentReturnRequested                bool
	resume                              *OwnerFuture[IteratorRequest[queryValue]]
	checkpointStarted, checkpointResult *OwnerFuture[struct{}]
	started, returning                  bool
	bodyFailure                         error
}

func newQueryIteratorBridge(scope *cooperative.Scope, owner *cooperative.Owner) (*queryIteratorBridge, error) {
	if owner == nil || !scope.BelongsTo(owner) {
		return nil, cooperative.ErrRestricted
	}
	bridge := &queryIteratorBridge{owner: owner}
	bridge.checkpointStarted, _ = NewOwnerFuture[struct{}](owner)
	bridge.checkpointResult, _ = NewOwnerFuture[struct{}](owner)
	step := func(next *cooperative.Scope, iterator *OwnerIterator[queryValue], request IteratorRequest[queryValue]) error {
		if err := iterator.HoldProducer(next); err != nil {
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
		return bridge.resume.Resolve(next, request)
	}
	var err error
	bridge.loop, err = NewOwnerIterator(owner, step)
	if err != nil {
		return nil, err
	}
	// Source query() delegates queryLoop, then explicitly returns its terminal.
	// Empty command discovery is the host's already validated finite profile.
	bridge.query, err = NewOwnerIterator(owner, func(next *cooperative.Scope, iterator *OwnerIterator[queryValue], request IteratorRequest[queryValue]) error {
		return iterator.Delegate(next, bridge.loop, request, func(next *cooperative.Scope, result IteratorResult[queryValue], request IteratorRequest[queryValue]) error {
			if request.Returning {
				return iterator.CompleteReturn(next)
			}
			return iterator.CompleteDefined(next, result.Value, result.Defined)
		}, func(next *cooperative.Scope, err error) error { return iterator.Fail(next, err) })
	})
	if err != nil {
		return nil, err
	}
	finish := func(next *cooperative.Scope, iterator *OwnerIterator[queryValue], request IteratorRequest[queryValue], failure error) error {
		// Only publish the actual finally invocation. The original Frame owns
		// checkpoint I/O; a reaction never starts another Frame or query stack.
		if err := bridge.checkpointStarted.Resolve(next, struct{}{}); err != nil {
			return err
		}
		return AwaitGenerator(next, iterator, bridge.checkpointResult, func(next *cooperative.Scope, _ struct{}) error {
			if failure != nil {
				return iterator.Fail(next, failure)
			}
			if request.Returning {
				return iterator.CompleteReturn(next)
			}
			return iterator.Complete(next)
		}, func(next *cooperative.Scope, err error) error { return iterator.Fail(next, err) })
	}
	bridge.history, err = NewOwnerIterator(owner, func(next *cooperative.Scope, iterator *OwnerIterator[queryValue], request IteratorRequest[queryValue]) error {
		return iterator.Delegate(next, bridge.query, request, func(next *cooperative.Scope, _ IteratorResult[queryValue], request IteratorRequest[queryValue]) error {
			return finish(next, iterator, request, nil)
		}, func(next *cooperative.Scope, err error) error { return finish(next, iterator, request, err) })
	})
	if err != nil {
		return nil, err
	}
	bridge.next, err = bridge.history.Next(scope)
	if err != nil {
		return nil, err
	}
	if !bridge.started {
		return nil, cap("original query demand did not start producer")
	}
	return bridge, nil
}

func settled(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// emit is the original RunControlled Frame's suspended queryLoop yield. The
// actual iterator result travels through query/fileHistory. ASK processing then
// runs back on this same stack, and the original producer resumes only after
// the next real demand reaches it. No copied list or second model consumer.
func (bridge *queryIteratorBridge) emit(scope *cooperative.Scope, value queryloop.Yield, consumer queryloop.ControlledEmit) (actionResult requestretry.NoticeAction, failure error) {
	if !scope.BelongsTo(bridge.owner) {
		return 0, cooperative.ErrRestricted
	}
	defer func() {
		if failure != nil {
			failure = bridge.administrativeFailure(scope, failure)
		}
	}()
	bridge.resume, _ = NewOwnerFuture[IteratorRequest[queryValue]](bridge.owner)
	if err := bridge.loop.Yield(scope, queryValue{Yield: value}, func(next *cooperative.Scope, iterator *OwnerIterator[queryValue], request IteratorRequest[queryValue]) error {
		if err := iterator.HoldProducer(next); err != nil {
			return err
		}
		bridge.returning = request.Returning
		return bridge.resume.Resolve(next, request)
	}); err != nil {
		return 0, err
	}
	// Source for-await consumes the next Promise via its own reaction. Done
	// alone is the producer settlement, before that consumer continuation.
	result, err := bridge.next.Await(scope)
	if err != nil {
		return 0, err
	}
	if result.Done || !result.Defined || result.Value.Terminal != nil {
		return 0, cap("query iterator yielded no original message")
	}
	action, bodyErr := consumer(result.Value.Yield)
	// runAgent's outer pending next skips stream/request-start yields. Its
	// queued return reaches query only after a real recordable message has been
	// recorded and exposed by that outer generator; the lost next still records.
	if bridge.agentReturnRequested && bridge.externalReturn == nil && nativeAgentIteratorExposes(result.Value.Yield) {
		bridge.externalReturn, err = bridge.history.ReturnUndefined(scope)
		if err != nil {
			return 0, err
		}
	}
	if bridge.externalReturn != nil {
		bridge.bodyFailure = bodyErr
		bridge.next = bridge.externalReturn
	} else if bodyErr != nil || action == requestretry.Stop {
		bridge.bodyFailure = bodyErr
		bridge.next, err = bridge.history.ReturnUndefined(scope)
	} else {
		bridge.next, err = bridge.history.Next(scope)
	}
	if err != nil {
		return 0, err
	}
	// A synchronous Next may already have reached the original suspended body.
	// Await only a genuinely pending demand receipt, never an extra settled await.
	if !settled(bridge.resume.Done()) {
		if err = scope.AwaitPromise(bridge.resume.Done()); err != nil {
			return 0, err
		}
	}
	request, err := bridge.resume.Wait(context.Background())
	if err != nil {
		return 0, err
	}
	if bodyErr != nil {
		return 0, bodyErr
	}
	if request.Returning {
		return requestretry.Stop, nil
	}
	return action, nil
}

func (bridge *queryIteratorBridge) finish(scope *cooperative.Scope, outcome queryloop.Outcome, queryErr error, checkpoint func() error) (failure error) {
	if !scope.BelongsTo(bridge.owner) {
		return cooperative.ErrRestricted
	}
	defer func() {
		if failure != nil {
			failure = bridge.administrativeFailure(scope, failure)
		}
	}()
	// A failed Scope handoff cannot replace the original native administrative
	// error with its bare context cause. It also cannot erase a real ASK throw.
	handoffFailure := func(failure error) error {
		if bridge.bodyFailure != nil {
			return bridge.bodyFailure
		}
		if provider.IsOwnerWaitFailure(queryErr) {
			return queryErr
		}
		return failure
	}
	var err error
	if bridge.returning && (queryErr == nil || bridge.bodyFailure != nil) {
		err = bridge.loop.CompleteReturn(scope)
	} else if queryErr != nil {
		err = bridge.loop.Fail(scope, queryErr)
	} else {
		err = bridge.loop.CompleteValue(scope, queryValue{Terminal: &outcome})
	}
	if err != nil {
		return handoffFailure(err)
	}
	if err = scope.AwaitPromise(bridge.checkpointStarted.Done()); err != nil {
		return handoffFailure(err)
	}
	if _, err = bridge.checkpointStarted.Wait(context.Background()); err != nil {
		return handoffFailure(err)
	}
	checkpointErr := checkpoint()
	if checkpointErr != nil {
		err = bridge.checkpointResult.Reject(scope, checkpointErr)
	} else {
		err = bridge.checkpointResult.Resolve(scope, struct{}{})
	}
	if err != nil {
		// The original finally may have thrown before its receipt could settle.
		// Keep that throw, subject to AsyncIteratorClose's body-throw precedence.
		if checkpointErr != nil {
			if bridge.bodyFailure != nil {
				return bridge.bodyFailure
			}
			return checkpointErr
		}
		return handoffFailure(err)
	}
	result, err := bridge.next.Await(scope)
	// AsyncIteratorClose preserves a body throw even when iterator.return/finally
	// rejects. An inner query throw instead permits finally rejection to replace it.
	if bridge.bodyFailure != nil {
		return bridge.bodyFailure
	}
	if err != nil {
		return handoffFailure(err)
	}
	if !result.Done {
		return cap("query completion retained an unexpected yield")
	}
	return nil
}

func (h *Host) runQueryWrapped(scope *cooperative.Scope, ctx context.Context, history []queryloop.Message, options queryloop.Options, host queryloop.Host, consumer queryloop.ControlledEmit, checkpoint func() error) (queryloop.Outcome, error) {
	bridge, err := newQueryIteratorBridge(scope, h.options.Owner)
	if err != nil {
		return queryloop.Outcome{}, err
	}
	if h.isAgent() {
		h.agentQueryIterator = bridge
		defer func() { h.agentQueryIterator = nil }()
		if h.options.Print.NativeProcess && h.options.Print.AgentIteratorReady != nil {
			if err := h.options.Print.AgentIteratorReady(scope); err != nil {
				return queryloop.Outcome{}, bridge.finish(scope, queryloop.Outcome{}, err, checkpoint)
			}
		}
	}
	outcome, queryErr := queryloop.RunControlled(ctx, history, options, host, func(value queryloop.Yield) (requestretry.NoticeAction, error) {
		return bridge.emit(scope, value, consumer)
	})
	failure := bridge.finish(scope, outcome, queryErr, checkpoint)
	if bridge.agentReturnReceipt != nil {
		var e error
		if failure != nil {
			e = bridge.agentReturnReceipt.Reject(scope, failure)
		} else {
			e = bridge.agentReturnReceipt.Resolve(scope, struct{}{})
		}
		if e != nil {
			_ = bridge.agentReturnReceipt.RejectAdministrative(e)
		}
	}
	return outcome, failure
}
