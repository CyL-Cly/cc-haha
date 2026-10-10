package ownerasync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestSyncIteratorOwnerAdmissionBeforeProducerEffects(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	calls := 0
	producer := &syncProducerFixture{owner: owner, next: func(scope *cooperative.Scope, _ SyncNextRequest) (SyncIteratorResult[any], error) {
		calls++
		if err := scope.Frame(cooperative.FrameFeed, func() error { return nil }); !errors.Is(err, cooperative.ErrRestricted) {
			return SyncIteratorResult[any]{}, errors.New("sync callback acquired provider Frame")
		}
		return SyncIteratorResult[any]{IteratorResult: IteratorResult[any]{Done: true}}, nil
	}, returning: func(*cooperative.Scope, SyncReturnRequest[any]) (SyncIteratorResult[any], error) {
		calls++
		return SyncIteratorResult[any]{IteratorResult: IteratorResult[any]{Done: true}}, nil
	}}
	for _, pair := range []struct {
		owner    *cooperative.Owner
		producer OwnerSyncIterator[any]
	}{{nil, producer}, {owner, nil}, {foreign, producer}, {owner, (*syncProducerFixture)(nil)}} {
		if _, err := NewAsyncFromSync(pair.owner, pair.producer); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal(err)
		}
	}
	adapter, _ := NewAsyncFromSync[any](owner, producer)
	if adapter.AsyncIteratorOwner() != owner {
		t.Fatal("owner identity changed")
	}
	var nilAdapter *AsyncFromSync[any]
	if nilAdapter.AsyncIteratorOwner() != nil {
		t.Fatal("nil owner changed")
	}
	if _, err := nilAdapter.Next(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	var stale *cooperative.Scope
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		stale = scope
		demand, err := adapter.Next(scope)
		if err != nil {
			return err
		}
		_, err = demand.Await(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ReturnUndefined(stale); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := adapter.Return(scope, "foreign"); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign return admitted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("restricted admissions executed producer %d times", calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		cancel()
		_, err := adapter.Next(scope)
		if !errors.Is(err, context.Canceled) {
			return errors.New("canceled scope admitted")
		}
		return nil
	}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("canceled admission executed producer")
	}
}

func TestSyncIteratorUndefinedPresenceAndAliases(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	nextArgs := []bool{}
	returns := []SyncReturnRequest[any]{}
	value := &struct{ Text string }{"original"}
	producer := &syncProducerFixture{owner: owner, next: func(_ *cooperative.Scope, request SyncNextRequest) (SyncIteratorResult[any], error) {
		nextArgs = append(nextArgs, request.ArgumentPresent)
		return SyncIteratorResult[any]{IteratorResult: IteratorResult[any]{Defined: true, Value: value}}, nil
	}, returning: func(_ *cooperative.Scope, request SyncReturnRequest[any]) (SyncIteratorResult[any], error) {
		returns = append(returns, request)
		return SyncIteratorResult[any]{IteratorResult: IteratorResult[any]{Done: true, Defined: request.Defined, Value: request.Value}}, nil
	}}
	adapter, _ := NewAsyncFromSync[any](owner, producer)
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		one, _ := adapter.Next(scope)
		result, err := one.Await(scope)
		if err != nil || result.Value != value {
			return errors.New("original object identity changed")
		}
		value.Text = "mutated"
		two, _ := adapter.NextUndefined(scope)
		result, err = two.Await(scope)
		if err != nil || result.Value.(*struct{ Text string }).Text != "mutated" {
			return errors.New("reference mutation lost")
		}
		undefined, _ := adapter.ReturnUndefined(scope)
		result, err = undefined.Await(scope)
		if err != nil || !result.Done || result.Defined {
			return errors.New("undefined return changed")
		}
		explicitNull, _ := adapter.Return(scope, nil)
		result, err = explicitNull.Await(scope)
		if err != nil || !result.Done || !result.Defined || result.Value != nil {
			return errors.New("defined null return lost")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(nextArgs, []bool{false, true}) || len(returns) != 2 || !returns[0].ArgumentPresent || returns[0].Defined || !returns[1].ArgumentPresent || !returns[1].Defined {
		t.Fatalf("%v %v", nextArgs, returns)
	}
}

func TestSyncIteratorThrownMethodsAndInvalidPromiseRejectDemand(t *testing.T) {
	for _, mode := range []string{"throw-next", "throw-return", "foreign-promise", "undefined-promise"} {
		t.Run(mode, func(t *testing.T) {
			owner, other := cooperative.New(), cooperative.New()
			defer owner.Close()
			defer other.Close()
			original := errors.New("original synchronous failure")
			var value *OwnerFuture[IteratorValue[any]]
			if mode == "foreign-promise" {
				value, _ = NewOwnerFuture[IteratorValue[any]](other)
			} else {
				value, _ = NewOwnerFuture[IteratorValue[any]](owner)
			}
			producer := &syncProducerFixture{owner: owner, next: func(*cooperative.Scope, SyncNextRequest) (SyncIteratorResult[any], error) {
				if mode == "throw-next" {
					return SyncIteratorResult[any]{}, original
				}
				return SyncIteratorResult[any]{IteratorResult: IteratorResult[any]{Defined: mode != "undefined-promise"}, PromiseValue: value}, nil
			}, returning: func(*cooperative.Scope, SyncReturnRequest[any]) (SyncIteratorResult[any], error) {
				return SyncIteratorResult[any]{}, original
			}}
			adapter, _ := NewAsyncFromSync[any](owner, producer)
			if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				var demand *OwnerFuture[IteratorResult[any]]
				var err error
				if mode == "throw-return" {
					demand, err = adapter.ReturnUndefined(scope)
				} else {
					demand, err = adapter.Next(scope)
				}
				if err != nil {
					return errors.New("synchronous producer throw escaped result promise")
				}
				_, failure := demand.Await(scope)
				if mode == "throw-next" || mode == "throw-return" {
					if failure != original {
						return errors.New("original error identity changed")
					}
				} else if !errors.Is(failure, cooperative.ErrRestricted) {
					return errors.New("unsupported native promise admitted")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSyncIteratorPendingResultRejectedByOwnerClose(t *testing.T) {
	owner := cooperative.New()
	var demand *OwnerFuture[IteratorResult[any]]
	value, _ := NewOwnerFuture[IteratorValue[any]](owner)
	producer := &syncProducerFixture{owner: owner, next: func(*cooperative.Scope, SyncNextRequest) (SyncIteratorResult[any], error) {
		return SyncIteratorResult[any]{IteratorResult: IteratorResult[any]{Defined: true}, PromiseValue: value}, nil
	}, returning: func(*cooperative.Scope, SyncReturnRequest[any]) (SyncIteratorResult[any], error) {
		return SyncIteratorResult[any]{}, errors.New("unexpected rejection-close before Owner lifetime ends")
	}}
	adapter, _ := NewAsyncFromSync[any](owner, producer)
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error { var err error; demand, err = adapter.Next(scope); return err }); err != nil {
		t.Fatal(err)
	}
	owner.Close()
	_, failure := demand.Wait(context.Background())
	if !errors.Is(failure, cooperative.ErrClosed) {
		t.Fatal(failure)
	}
}

func TestSyncIteratorRejectedReturnDoesNotCloseProducerAgain(t *testing.T) {
	// This count and identity are original pinned Bun language observations in
	// sync_return_rejection_source.json, never a Go-produced expected result.
	raw, err := os.ReadFile("testdata/sync_return_rejection_source.json")
	if err != nil {
		t.Fatal(err)
	}
	var source []json.RawMessage
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	var final []any
	_ = json.Unmarshal(source[len(source)-1], &final)
	expectedReturns := int(final[1].(float64))
	owner := cooperative.New()
	defer owner.Close()
	original := errors.New("original-failure")
	calls := 0
	producer := &syncProducerFixture{owner: owner,
		next: func(*cooperative.Scope, SyncNextRequest) (SyncIteratorResult[any], error) {
			return SyncIteratorResult[any]{IteratorResult: IteratorResult[any]{Defined: true, Value: "one"}}, nil
		},
		returning: func(scope *cooperative.Scope, request SyncReturnRequest[any]) (SyncIteratorResult[any], error) {
			calls++
			value, _ := NewOwnerFuture[IteratorValue[any]](owner)
			_ = value.Reject(scope, original)
			return SyncIteratorResult[any]{IteratorResult: IteratorResult[any]{Defined: true}, PromiseValue: value}, nil
		},
	}
	adapter, _ := NewAsyncFromSync[any](owner, producer)
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		demand, err := adapter.Return(scope, "return-value")
		if err != nil {
			return err
		}
		_, err = demand.Await(scope)
		if err != original {
			return errors.New("return rejection changed original identity")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != expectedReturns {
		t.Fatalf("Source called sync.return %d time(s); actual %d", expectedReturns, calls)
	}
}

func TestSyncIteratorDelegateRejectsUnportedUndefinedYield(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	producer := &syncProducerFixture{owner: owner,
		next: func(*cooperative.Scope, SyncNextRequest) (SyncIteratorResult[any], error) {
			return SyncIteratorResult[any]{}, nil
		},
		returning: func(*cooperative.Scope, SyncReturnRequest[any]) (SyncIteratorResult[any], error) {
			return SyncIteratorResult[any]{IteratorResult: IteratorResult[any]{Done: true}}, nil
		},
	}
	adapter, _ := NewAsyncFromSync[any](owner, producer)
	outer, _ := NewOwnerIterator(owner, func(scope *cooperative.Scope, iterator *OwnerIterator[any], request IteratorRequest[any]) error {
		return iterator.Delegate(scope, adapter, request, func(*cooperative.Scope, IteratorResult[any], IteratorRequest[any]) error {
			return errors.New("undefined non-done mistaken for completion")
		}, nil)
	})
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		demand, err := outer.Next(scope)
		if err != nil {
			return err
		}
		_, err = demand.Await(scope)
		if !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("unported undefined yield silently mapped to zero")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
