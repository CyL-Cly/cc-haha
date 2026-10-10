package sdkhost

import (
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkoutput"
)

// ConsumerAdministrativeFailure is a host Scope failure. It is not a model
// APIUserAbortError, a normal iterator return, or an original ASK body throw.
type ConsumerAdministrativeFailure struct{ Cause error }

func (failure *ConsumerAdministrativeFailure) Error() string {
	return "query consumer administration: " + failure.Cause.Error()
}
func (failure *ConsumerAdministrativeFailure) Unwrap() error { return failure.Cause }
func IsConsumerAdministrativeFailure(err error) bool {
	var failure *ConsumerAdministrativeFailure
	return errors.As(err, &failure)
}

func consumerAdministration(err error) error {
	if provider.IsOwnerWaitFailure(err) || IsConsumerAdministrativeFailure(err) {
		return err
	}
	return &ConsumerAdministrativeFailure{Cause: err}
}
func scopeAdministration(scope *cooperative.Scope, owner *cooperative.Owner) error {
	if !scope.BelongsTo(owner) {
		return nil
	}
	return scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
}
func rejectConsumerFuture[T any](future *OwnerFuture[T], failure error) {
	if future != nil {
		_ = future.RejectAdministrative(failure)
	}
}

// Abandon only this bridge's consumer obligations on its canonical Owner.
// No producer Frame, checkpoint I/O, native abort, or Record join is run here.
func (bridge *queryIteratorBridge) abandonAdministrative(failure error) {
	// The callback is owner-lifetime independent of the cancelled caller. It runs
	// after the original stack unwinds, and cannot start a replacement ASK.
	_, err := bridge.owner.Microtask(cooperative.Read, func(scope *cooperative.Scope) error {
		for _, iterator := range []*OwnerIterator[queryValue]{bridge.loop, bridge.query, bridge.history} {
			if err := iterator.Abandon(scope, failure); err != nil {
				return err
			}
		}
		rejectConsumerFuture(bridge.next, failure)
		rejectConsumerFuture(bridge.resume, failure)
		rejectConsumerFuture(bridge.checkpointStarted, failure)
		rejectConsumerFuture(bridge.checkpointResult, failure)
		return nil
	})
	if err != nil {
		// A closed Owner cannot execute metadata callbacks. Immutable obligations
		// must still reject; no business or iterator mutation escapes onto a worker.
		rejectConsumerFuture(bridge.next, failure)
		rejectConsumerFuture(bridge.resume, failure)
		rejectConsumerFuture(bridge.checkpointStarted, failure)
		rejectConsumerFuture(bridge.checkpointResult, failure)
	}
}
func (bridge *queryIteratorBridge) administrativeFailure(scope *cooperative.Scope, failure error) error {
	cause := scopeAdministration(scope, bridge.owner)
	if cause == nil {
		return failure
	}
	if bridge.bodyFailure != nil {
		failure = bridge.bodyFailure
	} else if !provider.IsOwnerWaitFailure(failure) && !IsConsumerAdministrativeFailure(failure) && errors.Is(failure, cause) {
		failure = consumerAdministration(cause)
	}
	bridge.abandonAdministrative(failure)
	return failure
}

// The sync normalizer has no native lease or Frame finally. Its suspended
// producer and adapter receipt still belong to the original canonical Owner.
func (h *Host) abandonNormalizer(producer *syncNormalizer, future *OwnerFuture[IteratorResult[*sdkoutput.View]], failure error) {
	_, err := h.options.Owner.Microtask(cooperative.Read, func(*cooperative.Scope) error {
		producer.producer.Return()
		rejectConsumerFuture(future, failure)
		return nil
	})
	if err != nil {
		rejectConsumerFuture(future, failure)
	}
}
