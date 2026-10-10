package diagnostics

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// TimingResult is the caller's actual immediate value or already-created
// same-Owner Promise. Immediate values still resume on one await reaction.
type TimingResult[T any] struct {
	value    T
	future   *ownerasync.OwnerFuture[T]
	isFuture bool
}

func TimingValue[T any](value T) TimingResult[T] { return TimingResult[T]{value: value} }
func TimingFuture[T any](future *ownerasync.OwnerFuture[T]) TimingResult[T] {
	return TimingResult[T]{future: future, isFuture: true}
}

// TimingData spreads typed own properties only when the helper constructs the
// completion data, after its duration clock. It may report a caller's original
// property-access error. This is not an arbitrary JavaScript object runtime.
type TimingData func(*cooperative.Scope, *protocol.JSONObject) error

func TimingObject(value protocol.JSONObject) TimingData {
	return func(_ *cooperative.Scope, target *protocol.JSONObject) error {
		target.Assign(value)
		return nil
	}
}

// WithTiming borrows the existing process sink, clocks, and caller Owner. The
// start log and fn execute before returning a fresh Promise. A synchronous fn
// error logs failure before returning; settlement and getData resume on the
// real await reaction. No business goroutine, Frame or extra Owner is created.
// The second return is only an administrative admission error. Business errors
// reject the returned Promise, retaining identity unless failure logging throws.
func WithTiming[T any](scope *cooperative.Scope, owner *cooperative.Owner, process *Process, event string,
	fn func(*cooperative.Scope) (TimingResult[T], error), getData func(*cooperative.Scope, T) (TimingData, error),
) (*ownerasync.OwnerFuture[T], error) {
	if !scope.BelongsTo(owner) || process == nil || fn == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return nil, err
	}
	returned, err := ownerasync.NewOwnerFuture[T](owner)
	if err != nil {
		return nil, err
	}
	start := float64(process.options.NowMillis())
	if err := process.Log("info", event+"_started", nil); err != nil {
		if err := returned.Reject(scope, err); err != nil {
			_ = returned.RejectAdministrative(err)
			return nil, err
		}
		return returned, nil
	}
	fail := func(next *cooperative.Scope, failure error) error {
		data := protocol.NewJSONObject(protocol.JSONField{Name: "duration_ms", Value: Number(float64(process.options.NowMillis()) - start)})
		if err := process.Log("error", event+"_failed", &data); err != nil {
			failure = err
		}
		return returned.Reject(next, failure)
	}
	resume := func(next *cooperative.Scope, value T, failure error) error {
		if failure != nil {
			return fail(next, failure)
		}
		var additional TimingData
		if getData != nil {
			var err error
			additional, err = getData(next, value)
			if err != nil {
				return fail(next, err)
			}
		}
		data := protocol.NewJSONObject(protocol.JSONField{Name: "duration_ms", Value: Number(float64(process.options.NowMillis()) - start)})
		if additional != nil {
			if err := additional(next, &data); err != nil {
				return fail(next, err)
			}
		}
		if err := process.Log("info", event+"_completed", &data); err != nil {
			return fail(next, err)
		}
		return returned.Resolve(next, value)
	}
	result, failure := fn(scope)
	if failure != nil {
		if err := fail(scope, failure); err != nil {
			_ = returned.RejectAdministrative(err)
			return nil, err
		}
		return returned, nil
	}
	if result.isFuture {
		_, err = result.future.Then(scope, resume)
	} else {
		_, err = owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error {
			return resume(next, result.value, nil)
		})
	}
	if err != nil {
		_ = returned.RejectAdministrative(err)
		return nil, err
	}
	return returned, nil
}
