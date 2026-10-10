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

func TestOwnerFutureActualBunCompletionPrograms(t *testing.T) {
	data, err := os.ReadFile("testdata/owner_completion_source.json")
	if err != nil {
		t.Fatal(err)
	}
	var originals []primitiveObservation
	if err := json.Unmarshal(data, &originals); err != nil {
		t.Fatal(err)
	}
	for _, expected := range originals {
		t.Run(expected.Input.Profile, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			actual := primitiveObservation{Input: expected.Input, Events: []string{}, Results: []IteratorResult[string]{}}
			event := func(value string) { actual.Events = append(actual.Events, value) }
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if expected.Input.Profile == "promise-resolution-adoption" {
					source, _ := NewOwnerFuture[string](owner)
					target, _ := NewOwnerFuture[string](owner)
					_, _ = target.Then(scope, func(_ *cooperative.Scope, value string, _ error) error { event("target:" + value); return nil })
					_ = source.Resolve(scope, "one")
					_, _ = source.Then(scope, func(_ *cooperative.Scope, value string, _ error) error { event("source:" + value); return nil })
					if err := target.ResolveFrom(scope, source); err != nil {
						return err
					}
					_, _ = owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { event("independent:peer"); return nil })
					if _, err := target.Await(scope); err != nil {
						return err
					}
					event("target:await")
					return nil
				}
				names := []string{"normal", "return"}
				if expected.Input.Profile == "return-resumption-await" {
					names = []string{"abrupt"}
				}
				for _, name := range names {
					iterator, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
						return iterator.Yield(next, "one", func(later *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
							if name == "abrupt" {
								event("body:finally")
								return iterator.CompleteReturn(later)
							}
							event(name + ":body-done")
							if name == "return" {
								return iterator.CompleteValue(later, "final")
							}
							return iterator.Complete(later)
						})
					})
					first, err := iterator.Next(scope)
					if err != nil {
						return err
					}
					if _, err := first.Await(scope); err != nil {
						return err
					}
					var last *OwnerFuture[IteratorResult[string]]
					if name == "abrupt" {
						last, err = iterator.Return(scope, "final")
					} else {
						last, err = iterator.Next(scope)
					}
					if err != nil {
						return err
					}
					completion, err := last.Then(scope, func(_ *cooperative.Scope, value IteratorResult[string], failure error) error {
						if failure != nil {
							return failure
						}
						if name == "abrupt" {
							event("consumer:return")
						} else {
							event(name + ":consumer")
						}
						actual.Results = append(actual.Results, value)
						return nil
					})
					if err != nil {
						return err
					}
					_, _ = owner.Microtask(cooperative.Read, func(*cooperative.Scope) error {
						if name == "abrupt" {
							event("independent:peer")
						} else {
							event(name + ":peer")
						}
						return nil
					})
					if name == "abrupt" {
						event("caller:return-returned")
					}
					if _, err := completion.Await(scope); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("actual=%+v\nSource=%+v", actual, expected)
			}
		})
	}
}

func TestOwnerFutureAwaitStopsAtItsOwnReactionAndPreservesReferences(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	value := &struct{ Value string }{"original"}
	events := []string{}
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		future, _ := NewOwnerFuture[*struct{ Value string }](owner)
		_ = future.Resolve(scope, value)
		_, _ = future.Then(scope, func(next *cooperative.Scope, got *struct{ Value string }, _ error) error {
			if got != value {
				return errors.New("reference copied")
			}
			events = append(events, "early")
			_, err := future.Then(next, func(*cooperative.Scope, *struct{ Value string }, error) error {
				events = append(events, "nested")
				return nil
			})
			return err
		})
		got, err := future.Await(scope)
		if err != nil {
			return err
		}
		if got != value {
			return errors.New("await reference copied")
		}
		events = append(events, "after-await")
		_, err = future.Await(scope)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"early", "after-await", "nested"}) {
		t.Fatal(events)
	}
}

func TestOwnerFutureCloseRejectsPendingAndQueuedDependentObligations(t *testing.T) {
	owner := cooperative.New()
	var pending *OwnerFuture[string]
	var dependent *OwnerFuture[struct{}]
	ran := false
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		pending, _ = NewOwnerFuture[string](owner)
		dependent, _ = pending.Then(scope, func(*cooperative.Scope, string, error) error { ran = true; return nil })
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	owner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := pending.Wait(ctx); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := dependent.Wait(ctx); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("administrative Close ran a business reaction")
	}
}

func TestOwnerFutureRejectAdoptionAndSameOwnerGuards(t *testing.T) {
	owner, other := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer other.Close()
	if _, err := NewOwnerFuture[string](nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	var future *OwnerFuture[string]
	var stale *cooperative.Scope
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		stale = scope
		future, _ = NewOwnerFuture[string](owner)
		if _, err := future.Then(scope, nil); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("nil reaction accepted")
		}
		if err := future.Reject(scope, nil); err == nil {
			return errors.New("nil error accepted")
		}
		if err := future.ResolveFrom(scope, future); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("self adoption accepted")
		}
		if err := future.ResolveFrom(scope, nil); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("nil adoption accepted")
		}
		source, _ := NewOwnerFuture[string](owner)
		original := errors.New("original failure")
		_ = source.Reject(scope, original)
		if err := future.ResolveFrom(scope, source); err != nil {
			return err
		}
		_ = future.Resolve(scope, "ignored")
		if err := future.ResolveFrom(scope, source); err != nil {
			return err
		}
		_, failure := future.Await(scope)
		if failure != original {
			return errors.New("rejection identity changed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := future.Resolve(stale, "bad"); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	err = other.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := future.Resolve(scope, "foreign"); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign resolution accepted")
		}
		if _, err := future.Then(scope, func(*cooperative.Scope, string, error) error { return nil }); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign reaction accepted")
		}
		foreign, _ := NewOwnerFuture[string](other)
		if err := foreign.ResolveFrom(scope, future); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign adoption accepted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := future.Wait(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := future.Wait(ctx); err != nil && !errors.Is(err, context.Canceled) && err.Error() != "original failure" {
		t.Fatal(err)
	}
}

func TestOwnerFutureAwaitCancellationLeavesIndependentReceiptAlive(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	var future *OwnerFuture[string]
	ctx, cancel := context.WithCancel(context.Background())
	err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		future, _ = NewOwnerFuture[string](owner)
		cancel()
		_, err := future.Await(scope)
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error { return future.Resolve(scope, "original completion") })
	if err != nil {
		t.Fatal(err)
	}
	value, err := future.Wait(context.Background())
	if err != nil || value != "original completion" {
		t.Fatal(value, err)
	}
}

func TestOwnerFutureHandlerFailureAndCanceledAdministrativeWait(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	var pending *OwnerFuture[string]
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		parent, _ := NewOwnerFuture[string](owner)
		_ = parent.Resolve(scope, "original")
		original := errors.New("reaction failed")
		child, err := parent.Then(scope, func(*cooperative.Scope, string, error) error { return original })
		if err != nil {
			return err
		}
		_, failure := child.Await(scope)
		if failure != original {
			return errors.New("handler rejection identity lost")
		}
		pending, _ = NewOwnerFuture[string](owner)
		var absent *OwnerFuture[string]
		if err := absent.ResolveFrom(scope, parent); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("nil receiver admitted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pending.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := pending.Await(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
}

func TestOwnerFutureCanceledScopeCannotResolveIndependentObligation(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	ctx, cancel := context.WithCancel(context.Background())
	original := errors.New("cancel independent observer")
	var future *OwnerFuture[string]
	err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		future, _ = NewOwnerFuture[string](owner)
		source, _ := NewOwnerFuture[string](owner)
		cancel()
		if err := future.Reject(scope, original); !errors.Is(err, context.Canceled) {
			return errors.New("canceled rejection admitted")
		}
		if err := future.ResolveFrom(scope, source); !errors.Is(err, context.Canceled) {
			return errors.New("canceled adoption admitted")
		}
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_ = future.Resolve(scope, "fresh")
		_ = future.ResolveFrom(scope, future)
		_ = future.Reject(scope, original)
		value, failure := future.Await(scope)
		if failure != nil || value != "fresh" {
			return errors.New("canceled observer stole independent future")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
