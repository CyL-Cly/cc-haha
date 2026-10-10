package ownerasync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestOwnerIteratorHoldProducerKeepsFramesOnOriginalStack(t *testing.T) {
	for _, returning := range []bool{false, true} {
		t.Run(map[bool]string{false: "Next", true: "Return"}[returning], func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			events := []string{}
			err := owner.Run(ctx, func(scope *cooperative.Scope) error {
				started, _ := NewOwnerFuture[IteratorRequest[string]](owner)
				resumed, _ := NewOwnerFuture[IteratorRequest[string]](owner)
				iterator, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, iterator *OwnerIterator[string], request IteratorRequest[string]) error {
					if err := iterator.HoldProducer(next); err != nil {
						return err
					}
					return started.Resolve(next, request)
				})
				first, err := iterator.Next(scope)
				if err != nil {
					return err
				}
				if err := scope.AwaitPromise(started.Done()); err != nil {
					return err
				}
				if err := scope.Frame(cooperative.FrameFeed, func() error { events = append(events, "original:first-frame"); return nil }); err != nil {
					return err
				}
				var last *OwnerFuture[IteratorResult[string]]
				_, err = first.Then(scope, func(next *cooperative.Scope, result IteratorResult[string], failure error) error {
					if failure != nil {
						return failure
					}
					if result.Value != "one" {
						return errors.New("first output changed")
					}
					events = append(events, "consumer:first")
					if err := next.Frame(cooperative.FrameFeed, func() error { return nil }); !errors.Is(err, cooperative.ErrRestricted) {
						return errors.New("reaction acquired Frame privilege")
					}
					var e error
					if returning {
						last, e = iterator.ReturnUndefined(next)
					} else {
						last, e = iterator.Next(next)
					}
					return e
				})
				if err != nil {
					return err
				}
				if err := iterator.Yield(scope, "one", func(next *cooperative.Scope, iterator *OwnerIterator[string], request IteratorRequest[string]) error {
					if err := iterator.HoldProducer(next); err != nil {
						return err
					}
					return resumed.Resolve(next, request)
				}); err != nil {
					return err
				}
				if err := scope.AwaitPromise(resumed.Done()); err != nil {
					return err
				}
				request, err := resumed.Wait(ctx)
				if err != nil {
					return err
				}
				if request.Returning != returning {
					return errors.New("resume demand changed")
				}
				if err := scope.Frame(cooperative.FrameFinish, func() error { events = append(events, "original:finally-frame"); return nil }); err != nil {
					return err
				}
				if returning {
					err = iterator.CompleteReturn(scope)
				} else {
					err = iterator.Complete(scope)
				}
				if err != nil {
					return err
				}
				result, err := last.Await(scope)
				if err != nil {
					return err
				}
				if !result.Done || result.Defined {
					return errors.New("bridge completion changed")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(events, []string{"original:first-frame", "consumer:first", "original:finally-frame"}) {
				t.Fatal(events)
			}
		})
	}
}

func TestOwnerIteratorHeldAndQueuedDemandsRejectOnClose(t *testing.T) {
	owner := cooperative.New()
	var first, second *OwnerFuture[IteratorResult[string]]
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		iterator, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
			return iterator.HoldProducer(next)
		})
		first, _ = iterator.Next(scope)
		second, _ = iterator.Next(scope)
		if iterator.active == nil || !iterator.held || len(iterator.queue) != 1 {
			return errors.New("physical producer demand lost")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	owner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, future := range []*OwnerFuture[IteratorResult[string]]{first, second} {
		if _, err := future.Wait(ctx); !errors.Is(err, cooperative.ErrClosed) {
			t.Fatal(err)
		}
	}
}

func TestOwnerIteratorGuardsAndContinuationErrors(t *testing.T) {
	owner, other := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer other.Close()
	if _, err := NewOwnerIterator[string](nil, nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	var iterator *OwnerIterator[string]
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		iterator, _ = NewOwnerIterator(owner, func(*cooperative.Scope, *OwnerIterator[string], IteratorRequest[string]) error {
			return errors.New("original continuation failure")
		})
		if err := iterator.Yield(scope, "early", nil); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("yield without demand accepted")
		}
		first, err := iterator.Next(scope)
		if err != nil {
			return err
		}
		_, failure := first.Await(scope)
		if failure == nil || failure.Error() != "original continuation failure" {
			return errors.New("continuation failure lost")
		}
		done, _ := iterator.Next(scope)
		value, err := done.Await(scope)
		if err != nil || !value.Done {
			return errors.New("failed generator stayed active")
		}
		if err := iterator.CompleteValue(scope, "after-done"); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("explicit completion after done accepted")
		}
		if err := iterator.Complete(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("completion after done accepted")
		}
		if err := iterator.CompleteReturn(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("completion without active return accepted")
		}
		invalid, _ := NewOwnerIterator(owner, func(*cooperative.Scope, *OwnerIterator[string], IteratorRequest[string]) error { return nil })
		failed, _ := invalid.Next(scope)
		_, err = failed.Await(scope)
		if err == nil {
			return errors.New("unfinished continuation accepted")
		}
		parked, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
			return iterator.HoldProducer(next)
		})
		_, _ = parked.Next(scope)
		if err := parked.HoldProducer(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("double producer hold accepted")
		}
		if err := parked.YieldFuture(scope, nil, nil, nil); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("nil yield future accepted")
		}
		if err := parked.Delegate(scope, parked, IteratorRequest[string]{}, nil, nil); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("self delegate accepted")
		}
		if err := AwaitGenerator[string, string](scope, parked, nil, nil, nil); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("nil await accepted")
		}
		if err := parked.Fail(scope, nil); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("nil failure accepted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = other.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := iterator.Next(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign iterator admitted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOwnerIteratorActualBunDelegateReturn(t *testing.T) {
	data, err := os.ReadFile("testdata/owner_return_delegate_source.json")
	if err != nil {
		t.Fatal(err)
	}
	var originals []primitiveObservation
	if err = json.Unmarshal(data, &originals); err != nil {
		t.Fatal(err)
	}
	for _, expected := range originals {
		t.Run(expected.Input.Profile, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			actual := primitiveObservation{Input: expected.Input, Events: []string{}, Results: []IteratorResult[string]{}}
			event := func(v string) { actual.Events = append(actual.Events, v) }
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				inner, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, it *OwnerIterator[string], _ IteratorRequest[string]) error {
					event("inner:start")
					return it.Yield(next, "one", func(later *cooperative.Scope, it *OwnerIterator[string], pending IteratorRequest[string]) error {
						event("inner:finally")
						if pending.Returning {
							return it.CompleteReturn(later)
						}
						return it.Complete(later)
					})
				})
				onDone := func(next *cooperative.Scope, it *OwnerIterator[string], pending IteratorRequest[string]) error {
					return it.Delegate(next, inner, pending, func(later *cooperative.Scope, _ IteratorResult[string], request IteratorRequest[string]) error {
						event("outer:finally")
						if request.Returning {
							return it.CompleteReturn(later)
						}
						return it.Complete(later)
					}, nil)
				}
				outer, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, it *OwnerIterator[string], pending IteratorRequest[string]) error {
					event("outer:start")
					return onDone(next, it, pending)
				})
				first, _ := outer.Next(scope)
				value, err := first.Await(scope)
				if err != nil {
					return err
				}
				event("consumer:first")
				actual.Results = append(actual.Results, value)
				var returning *OwnerFuture[IteratorResult[string]]
				if expected.Input.Profile == "delegate-return-defined" {
					returning, err = outer.Return(scope, "final")
				} else {
					returning, err = outer.ReturnUndefined(scope)
				}
				if err != nil {
					return err
				}
				event("caller:return-returned")
				value, err = returning.Await(scope)
				if err != nil {
					return err
				}
				event("consumer:return")
				actual.Results = append(actual.Results, value)
				done, _ := outer.Next(scope)
				value, err = done.Await(scope)
				if err != nil {
					return err
				}
				event("consumer:done")
				actual.Results = append(actual.Results, value)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("actual %v; Source %v", actual, expected)
			}
		})
	}
}

func TestOwnerIteratorForeignScopeCannotMutateActiveGenerator(t *testing.T) {
	owner, other := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer other.Close()
	var iterator *OwnerIterator[string]
	var future *OwnerFuture[string]
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		future, _ = NewOwnerFuture[string](owner)
		iterator, _ = NewOwnerIterator(owner, func(next *cooperative.Scope, it *OwnerIterator[string], _ IteratorRequest[string]) error {
			return it.HoldProducer(next)
		})
		_, err := iterator.Next(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := other.Run(context.Background(), func(scope *cooperative.Scope) error {
		operations := []func() error{func() error { return iterator.HoldProducer(scope) }, func() error { return iterator.Yield(scope, "foreign", nil) }, func() error { return iterator.YieldFuture(scope, future, nil, nil) }, func() error { return iterator.Complete(scope) }, func() error { return iterator.CompleteValue(scope, "foreign") }, func() error { return iterator.CompleteReturn(scope) }, func() error { return iterator.Fail(scope, errors.New("foreign")) }, func() error {
			return AwaitGenerator(scope, iterator, future, func(*cooperative.Scope, string) error { return nil }, nil)
		}, func() error { return iterator.Delegate(scope, iterator, IteratorRequest[string]{}, nil, nil) }}
		for _, operation := range operations {
			if err := operation(); !errors.Is(err, cooperative.ErrRestricted) {
				return errors.New("foreign generator mutation admitted")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := iterator.CompleteValue(scope, "fresh"); err != nil {
			return err
		}
		done, err := iterator.Next(scope)
		if err != nil {
			return err
		}
		value, failure := done.Await(scope)
		if failure != nil || !value.Done {
			return errors.New("foreign call damaged original generator")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerIteratorRejectedAwaitPreservesFailureAndFinally(t *testing.T) {
	for _, mode := range []string{"yield-default", "await-default", "await-catch"} {
		t.Run(mode, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			original := errors.New("original rejected value")
			caught := false
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				rejected, _ := NewOwnerFuture[string](owner)
				_ = rejected.Reject(scope, original)
				iterator, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, it *OwnerIterator[string], _ IteratorRequest[string]) error {
					if mode == "yield-default" {
						return it.YieldFuture(next, rejected, func(*cooperative.Scope, *OwnerIterator[string], IteratorRequest[string]) error {
							return errors.New("unreachable fulfilled resume")
						}, nil)
					}
					var catch func(*cooperative.Scope, error) error
					if mode == "await-catch" {
						catch = func(later *cooperative.Scope, failure error) error {
							caught = failure == original
							return it.Complete(later)
						}
					}
					return AwaitGenerator(next, it, rejected, func(*cooperative.Scope, string) error { return errors.New("unreachable fulfilled await") }, catch)
				})
				demand, err := iterator.Next(scope)
				if err != nil {
					return err
				}
				value, failure := demand.Await(scope)
				if mode == "await-catch" {
					if failure != nil || !value.Done || !caught {
						return errors.New("catch/finally result changed")
					}
				} else if failure != original {
					return errors.New("original failure identity changed")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOwnerIteratorObservationPreservesDefinedZeroAndUndefined(t *testing.T) {
	for _, item := range []struct {
		value IteratorResult[string]
		want  string
	}{{IteratorResult[string]{Done: true}, `{"done":true,"defined":false}`}, {IteratorResult[string]{Defined: true, Value: ""}, `{"done":false,"defined":true,"value":""}`}} {
		raw, err := json.Marshal(item.value)
		if err != nil || string(raw) != item.want {
			t.Fatalf("%s %v", raw, err)
		}
	}
}

func TestOwnerIteratorContinuationErrorsRejectActiveDemand(t *testing.T) {
	for _, mode := range []string{"fulfilled-continuation-error", "await-catch-error", "yield-catch-error"} {
		t.Run(mode, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			original := errors.New("original continuation error")
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				source, _ := NewOwnerFuture[string](owner)
				if mode == "fulfilled-continuation-error" {
					_ = source.Resolve(scope, "one")
				} else {
					_ = source.Reject(scope, errors.New("input rejected"))
				}
				iterator, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, it *OwnerIterator[string], _ IteratorRequest[string]) error {
					catch := func(*cooperative.Scope, error) error { return original }
					if mode == "yield-catch-error" {
						return it.YieldFuture(next, source, func(*cooperative.Scope, *OwnerIterator[string], IteratorRequest[string]) error { return nil }, catch)
					}
					return AwaitGenerator(next, it, source, func(*cooperative.Scope, string) error { return original }, catch)
				})
				demand, err := iterator.Next(scope)
				if err != nil {
					return err
				}
				// A real FIFO barrier after the queued await reaction. It identifies completion
				// of that reaction, not a guessed number of fulfilled awaits or timer ticks.
				barrier, _ := owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { return nil })
				if err := scope.AwaitPromise(barrier.Done()); err != nil {
					return err
				}
				select {
				case <-demand.Done():
				default:
					return errors.New("active Next stranded after continuation rejection")
				}
				_, failure := demand.Await(scope)
				if failure != original {
					return errors.New("active Next lost original error identity")
				}
				completed, _ := iterator.Next(scope)
				value, failure := completed.Await(scope)
				if failure != nil || !value.Done {
					return errors.New("failed generator did not complete")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
