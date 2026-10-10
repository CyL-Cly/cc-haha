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

type primitiveObservation struct {
	Input struct {
		Profile string `json:"profile"`
	} `json:"input"`
	Events  []string                 `json:"events"`
	Results []IteratorResult[string] `json:"results"`
}

func ownerPrimitive(profile string) (primitiveObservation, error) {
	owner := cooperative.New()
	defer owner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	observation := primitiveObservation{Events: []string{}, Results: []IteratorResult[string]{}}
	observation.Input.Profile = profile
	err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		event := func(value string) { observation.Events = append(observation.Events, value) }
		observe := func(label string, value IteratorResult[string]) {
			event(label)
			observation.Results = append(observation.Results, value)
		}
		resolved := func(value string) *OwnerFuture[string] {
			future, _ := NewOwnerFuture[string](owner)
			_ = future.Resolve(scope, value)
			return future
		}
		done := func(next *cooperative.Scope, iterator *OwnerIterator[string], request IteratorRequest[string]) error {
			if request.Returning {
				return iterator.CompleteReturn(next)
			}
			return iterator.Complete(next)
		}
		consume := func(iterator *OwnerIterator[string], label string) error {
			future, err := iterator.Next(scope)
			if err != nil {
				return err
			}
			value, err := future.Await(scope)
			if err == nil {
				observe(label, value)
			}
			return err
		}
		switch profile {
		case "yield-suspended-until-next", "return-before-start", "return-after-yield":
			iterator, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, iterator *OwnerIterator[string], request IteratorRequest[string]) error {
				event("body:start")
				return iterator.Yield(next, "one", func(later *cooperative.Scope, iterator *OwnerIterator[string], request IteratorRequest[string]) error {
					if !request.Returning {
						event("body:resumed")
					}
					event("body:finally")
					return done(later, iterator, request)
				})
			})
			if profile == "yield-suspended-until-next" {
				event("caller:next1")
				first, err := iterator.Next(scope)
				if err != nil {
					return err
				}
				event("caller:next1-returned")
				value, err := first.Await(scope)
				if err != nil {
					return err
				}
				observe("consumer:first", value)
				event("caller:next2")
				last, err := iterator.Next(scope)
				if err != nil {
					return err
				}
				event("caller:next2-returned")
				value, err = last.Await(scope)
				if err != nil {
					return err
				}
				observe("consumer:done", value)
				return nil
			}
			if profile == "return-after-yield" {
				if err := consume(iterator, "consumer:first"); err != nil {
					return err
				}
			}
			returning, err := iterator.Return(scope, "return-value")
			if err != nil {
				return err
			}
			event("caller:return-returned")
			value, err := returning.Await(scope)
			if err != nil {
				return err
			}
			observe("consumer:return", value)
			return consume(iterator, "consumer:done")
		case "queued-next-fifo":
			iterator, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
				event("body:start")
				return iterator.Yield(next, "one", func(later *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
					event("body:next")
					return iterator.Yield(later, "two", func(last *cooperative.Scope, iterator *OwnerIterator[string], request IteratorRequest[string]) error {
						event("body:done")
						return done(last, iterator, request)
					})
				})
			})
			var completion *OwnerFuture[struct{}]
			for _, label := range []string{"consumer:first", "consumer:second", "consumer:done"} {
				future, err := iterator.Next(scope)
				if err != nil {
					return err
				}
				completion, err = future.Then(scope, func(_ *cooperative.Scope, value IteratorResult[string], failure error) error {
					if failure != nil {
						return failure
					}
					observe(label, value)
					return nil
				})
				if err != nil {
					return err
				}
			}
			event("caller:all-requested")
			_, err := completion.Await(scope)
			return err
		case "yield-promise-value", "await-then-yield":
			iterator, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
				event("body:start")
				resume := func(later *cooperative.Scope, iterator *OwnerIterator[string], request IteratorRequest[string]) error {
					event("body:done")
					return done(later, iterator, request)
				}
				if profile == "await-then-yield" {
					return AwaitGenerator(next, iterator, resolved("one"), func(later *cooperative.Scope, value string) error {
						event("body:after-await")
						return iterator.Yield(later, value, resume)
					}, nil)
				}
				mapped, _ := NewOwnerFuture[string](owner)
				_, err := resolved("one").Then(next, func(later *cooperative.Scope, value string, failure error) error {
					event("value:then")
					return mapped.Resolve(later, value)
				})
				if err != nil {
					return err
				}
				return iterator.YieldFuture(next, mapped, resume, nil)
			})
			first, err := iterator.Next(scope)
			if err != nil {
				return err
			}
			completion, err := first.Then(scope, func(_ *cooperative.Scope, value IteratorResult[string], failure error) error {
				if failure != nil {
					return failure
				}
				observe("consumer:first", value)
				return nil
			})
			if err != nil {
				return err
			}
			if _, err := owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { event("independent:microtask"); return nil }); err != nil {
				return err
			}
			if _, err := completion.Await(scope); err != nil {
				return err
			}
			return consume(iterator, "consumer:done")
		case "delegate-two-layers", "delegate-error-finally":
			inner, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
				event("inner:start")
				if profile == "delegate-error-finally" {
					failed, _ := NewOwnerFuture[string](owner)
					_ = failed.Reject(next, errors.New("original-failure"))
					return iterator.YieldFuture(next, failed, done, func(later *cooperative.Scope, failure error) error {
						event("inner:finally")
						return iterator.Fail(later, failure)
					})
				}
				return iterator.Yield(next, "one", func(later *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
					event("inner:done")
					event("inner:finally")
					return iterator.CompleteValue(later, "final")
				})
			})
			layer := func(name string, inner *OwnerIterator[string]) *OwnerIterator[string] {
				outer, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, iterator *OwnerIterator[string], request IteratorRequest[string]) error {
					event(name + ":start")
					return iterator.Delegate(next, inner, request, func(later *cooperative.Scope, result IteratorResult[string], _ IteratorRequest[string]) error {
						event(name + ":done")
						event(name + ":finally")
						return iterator.CompleteValue(later, result.Value)
					}, func(later *cooperative.Scope, failure error) error {
						event(name + ":finally")
						return iterator.Fail(later, failure)
					})
				})
				return outer
			}
			var outer *OwnerIterator[string]
			if profile == "delegate-two-layers" {
				outer = layer("outer", layer("middle", inner))
			} else {
				outer = layer("outer", inner)
			}
			if profile == "delegate-error-finally" {
				first, err := outer.Next(scope)
				if err != nil {
					return err
				}
				_, failure := first.Await(scope)
				if failure == nil {
					return errors.New("expected original yield rejection")
				}
				event("consumer:error:" + failure.Error())
			} else if err := consume(outer, "consumer:first"); err != nil {
				return err
			}
			return consume(outer, "consumer:done")
		case "producer-layered-result-reaction":
			return layeredProducer(scope, owner, event)
		default:
			return errors.New("unknown original primitive profile")
		}
	})
	return observation, err
}

func layeredProducer(scope *cooperative.Scope, owner *cooperative.Owner, event func(string)) error {
	firstResult, _ := NewOwnerFuture[struct{}](owner)
	inputDone, _ := NewOwnerFuture[struct{}](owner)
	queryDone, _ := NewOwnerFuture[struct{}](owner)
	raw, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
		event("input:first")
		return iterator.Yield(next, "first", func(later *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
			return AwaitGenerator(later, iterator, firstResult, func(ready *cooperative.Scope, _ struct{}) error {
				event("input:second")
				return iterator.Yield(ready, "second", func(last *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
					event("input:EOF")
					return iterator.Complete(last)
				})
			}, nil)
		})
	})
	var read *OwnerIterator[string]
	var readNext GeneratorContinuation[string]
	readNext = func(next *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
		future, err := raw.Next(next)
		if err != nil {
			return err
		}
		return AwaitGenerator(next, iterator, future, func(later *cooperative.Scope, value IteratorResult[string]) error {
			if value.Done {
				return iterator.Complete(later)
			}
			split, _ := NewOwnerIterator(owner, func(parse *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
				parsed, _ := NewOwnerFuture[string](owner)
				_ = parsed.Resolve(parse, value.Value)
				return AwaitGenerator(parse, iterator, parsed, func(parsed *cooperative.Scope, value string) error {
					event("input:parsed:" + value)
					return iterator.Yield(parsed, value, func(last *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
						return iterator.Complete(last)
					})
				}, nil)
			})
			return iterator.Delegate(later, split, IteratorRequest[string]{}, func(last *cooperative.Scope, _ IteratorResult[string], request IteratorRequest[string]) error {
				return readNext(last, iterator, request)
			}, nil)
		}, nil)
	}
	read, _ = NewOwnerIterator(owner, readNext)
	submit, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
		event("query:submit")
		return iterator.Yield(next, "result", func(last *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
			event("query:submit-done")
			return iterator.Complete(last)
		})
	})
	wrap := func(inner *OwnerIterator[string], final string) *OwnerIterator[string] {
		iterator, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, iterator *OwnerIterator[string], request IteratorRequest[string]) error {
			return iterator.Delegate(next, inner, request, func(last *cooperative.Scope, _ IteratorResult[string], _ IteratorRequest[string]) error {
				event(final)
				return iterator.Complete(last)
			}, nil)
		})
		return iterator
	}
	wrapper := wrap(wrap(submit, "query:cache-finally"), "query:wrapper-finally")
	var takeQuery func(*cooperative.Scope) error
	takeQuery = func(next *cooperative.Scope) error {
		future, err := wrapper.Next(next)
		if err != nil {
			return err
		}
		_, err = future.Then(next, func(later *cooperative.Scope, value IteratorResult[string], failure error) error {
			if failure != nil {
				return failure
			}
			if value.Done {
				event("query:completed")
				return queryDone.Resolve(later, struct{}{})
			}
			event("query:FIFO:" + value.Value)
			_ = firstResult.Resolve(later, struct{}{})
			return takeQuery(later)
		})
		return err
	}
	var takeInput func(*cooperative.Scope) error
	takeInput = func(next *cooperative.Scope) error {
		future, err := read.Next(next)
		if err != nil {
			return err
		}
		_, err = future.Then(next, func(later *cooperative.Scope, value IteratorResult[string], failure error) error {
			if failure != nil {
				return failure
			}
			if value.Done {
				event("input:closed")
				return inputDone.Resolve(later, struct{}{})
			}
			event("input:admit:" + value.Value)
			if value.Value == "first" {
				if err := takeQuery(later); err != nil {
					return err
				}
			}
			return takeInput(later)
		})
		return err
	}
	if err := takeInput(scope); err != nil {
		return err
	}
	if _, err := inputDone.Await(scope); err != nil {
		return err
	}
	_, err := queryDone.Await(scope)
	return err
}

func TestOwnerIteratorActualBunPrograms(t *testing.T) {
	data, err := os.ReadFile("testdata/owner_iterator_source.json")
	if err != nil {
		t.Fatal(err)
	}
	var originals []primitiveObservation
	if err := json.Unmarshal(data, &originals); err != nil {
		t.Fatal(err)
	}
	for _, expected := range originals {
		t.Run(expected.Input.Profile, func(t *testing.T) {
			actual, err := ownerPrimitive(expected.Input.Profile)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("actual=%+v\nSource=%+v", actual, expected)
			}
		})
	}
}
