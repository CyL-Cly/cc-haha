package sdkhost

import (
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkoutput"
)

type syncNormalizer struct {
	owner    *cooperative.Owner
	producer *sdkoutput.MessageIterator
}

func (iterator *syncNormalizer) SyncIteratorOwner() *cooperative.Owner { return iterator.owner }

func (iterator *syncNormalizer) NextSync(scope *cooperative.Scope, _ SyncNextRequest) (SyncIteratorResult[*sdkoutput.View], error) {
	if !scope.BelongsTo(iterator.owner) {
		return SyncIteratorResult[*sdkoutput.View]{}, cooperative.ErrRestricted
	}
	view, done, err := iterator.producer.Next()
	return SyncIteratorResult[*sdkoutput.View]{IteratorResult: IteratorResult[*sdkoutput.View]{Done: done, Defined: !done, Value: view}}, err
}

func (iterator *syncNormalizer) ReturnSync(scope *cooperative.Scope, request SyncReturnRequest[*sdkoutput.View]) (SyncIteratorResult[*sdkoutput.View], error) {
	if !scope.BelongsTo(iterator.owner) {
		return SyncIteratorResult[*sdkoutput.View]{}, cooperative.ErrRestricted
	}
	iterator.producer.Return()
	return SyncIteratorResult[*sdkoutput.View]{IteratorResult: IteratorResult[*sdkoutput.View]{Done: true, Defined: request.Defined, Value: request.Value}}, nil
}

// normalizeYields is ASK's actual async yield* of the sync normalizeMessage.
// The original Frame stack awaits each adapter result, including done/undefined.
// Non-done retains the View until that await resumes, then the existing ASK
// yield and its consumer handle FIFO admission. No second model consumer or
// rendered Source output enters this path.
func (h *Host) normalizeYields(scope *cooperative.Scope, message sdkoutput.Message) (actionResult requestretry.NoticeAction, failure error) {
	producer := &syncNormalizer{owner: h.options.Owner, producer: h.normalizer.IterateMessage(message)}
	var awaited *OwnerFuture[IteratorResult[*sdkoutput.View]]
	var originalBodyFailure error
	defer func() {
		if failure == nil {
			return
		}
		if cause := scopeAdministration(scope, h.options.Owner); cause != nil {
			if originalBodyFailure != nil {
				failure = originalBodyFailure
			} else if !provider.IsOwnerWaitFailure(failure) && !IsConsumerAdministrativeFailure(failure) && errors.Is(failure, cause) {
				failure = consumerAdministration(cause)
			}
			h.abandonNormalizer(producer, awaited, failure)
		}
	}()
	adapter, err := NewAsyncFromSync[*sdkoutput.View](h.options.Owner, producer)
	if err != nil {
		return 0, err
	}
	for {
		future, err := adapter.NextUndefined(scope)
		if err != nil {
			return 0, err
		}
		awaited = future
		result, err := future.Await(scope)
		if err != nil {
			return 0, err
		}
		if result.Done {
			return requestretry.Resume, nil
		}
		data, err := result.Value.MarshalJSON()
		if err != nil {
			return 0, err
		}
		action, err := h.deliver(scope, data)
		if err != nil {
			// AsyncIteratorClose preserves the consumer body throw if return rejects.
			original := err
			originalBodyFailure = err
			if scopeAdministration(scope, h.options.Owner) == nil {
				returned, closeErr := adapter.ReturnUndefined(scope)
				if closeErr == nil {
					_, _ = returned.Await(scope)
				}
			}
			return 0, original
		}
		if action == requestretry.Stop {
			// The finite local consumer stop closes the sync producer with the
			// for-await return(undefined) shape. Defined outer return values and
			// transport return interception remain a separate host capability.
			returned, err := adapter.ReturnUndefined(scope)
			if err != nil {
				return 0, err
			}
			if _, err = returned.Await(scope); err != nil {
				return 0, err
			}
			return action, nil
		}
	}
}
