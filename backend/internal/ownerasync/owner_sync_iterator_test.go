package ownerasync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkoutput"
)

type syncProducerFixture struct {
	owner     *cooperative.Owner
	next      func(*cooperative.Scope, SyncNextRequest) (SyncIteratorResult[any], error)
	returning func(*cooperative.Scope, SyncReturnRequest[any]) (SyncIteratorResult[any], error)
}

func (producer *syncProducerFixture) SyncIteratorOwner() *cooperative.Owner {
	if producer == nil {
		return nil
	}
	return producer.owner
}
func (producer *syncProducerFixture) NextSync(scope *cooperative.Scope, request SyncNextRequest) (SyncIteratorResult[any], error) {
	return producer.next(scope, request)
}
func (producer *syncProducerFixture) ReturnSync(scope *cooperative.Scope, request SyncReturnRequest[any]) (SyncIteratorResult[any], error) {
	return producer.returning(scope, request)
}

type syncObservation struct {
	Input           json.RawMessage `json:"input"`
	Classification  string          `json:"classification,omitempty"`
	Events          []string        `json:"events"`
	MethodCalls     []any           `json:"methodCalls,omitempty"`
	IteratorResults []any           `json:"iteratorResults,omitempty"`
	Results         []any           `json:"results,omitempty"`
	Capabilities    any             `json:"capabilities,omitempty"`
}

func recordedResult(result IteratorResult[any], keys bool) any {
	record := map[string]any{"done": result.Done, "defined": result.Defined}
	if keys {
		record["keys"] = []string{"value", "done"}
	}
	if result.Defined {
		record["value"] = result.Value
	}
	return record
}

func syncProgram(t *testing.T, input json.RawMessage) syncObservation {
	t.Helper()
	var original struct {
		Profile string          `json:"profile"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(input, &original); err != nil {
		t.Fatal(err)
	}
	owner := cooperative.New()
	defer owner.Close()
	actual := syncObservation{Input: input, Events: []string{}, MethodCalls: []any{}, IteratorResults: []any{}}
	event := func(v string) { actual.Events = append(actual.Events, v) }
	profile := original.Profile
	if profile == "async-delegate-fallthrough" {
		actual.Results = []any{}
		actual.MethodCalls = nil
		actual.IteratorResults = nil
	} else {
		actual.Classification = "language semantics only"
		if len(original.Message) > 0 {
			actual.Classification = "actual exports + explicit original language composition (not QueryEngine.ask)"
		}
		actual.Capabilities = map[string]any{"blocked": []any{}, "secureReads": []any{}, "absentReads": []any{}}
	}
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var iterator *OwnerIterator[any]
		if profile == "language-async-fallthrough" || profile == "async-delegate-fallthrough" {
			inner, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, it *OwnerIterator[any], _ IteratorRequest[any]) error {
				event("inner:start")
				return it.Yield(next, "one", func(later *cooperative.Scope, it *OwnerIterator[any], _ IteratorRequest[any]) error {
					event("inner:done")
					return it.Complete(later)
				})
			})
			iterator = inner
			if profile == "async-delegate-fallthrough" {
				iterator, _ = NewOwnerIterator(owner, func(next *cooperative.Scope, it *OwnerIterator[any], request IteratorRequest[any]) error {
					event("outer:start")
					return it.Delegate(next, inner, request, func(later *cooperative.Scope, _ IteratorResult[any], _ IteratorRequest[any]) error {
						event("outer:after")
						return it.Complete(later)
					}, nil)
				})
			}
		} else {
			isActual := len(original.Message) > 0
			name := "sync"
			if isActual {
				name = "normalize"
			}
			begun, finished := false, false
			var views []*sdkoutput.View
			var normalizer *sdkoutput.Normalizer
			if isActual {
				factory, _ := messages.NewFactory(func() string { panic("unexpected fixture assistant UUID allocation") }, func() time.Time { panic("unexpected fixture assistant time allocation") })
				var err error
				normalizer, err = sdkoutput.New(sdkoutput.Dependencies{Factory: factory, SessionID: func() json.RawMessage { return json.RawMessage(`"00000000-0000-4000-0000-000000000022"`) }, NowMillis: func() float64 { return 1700000000000 }, OriginalEnv: func(string) string { return "" }})
				if err != nil {
					return err
				}
			}
			index := 0
			producer := &syncProducerFixture{owner: owner}
			recordMethod := func(method string, args []any, result SyncIteratorResult[any]) {
				plain := result.IteratorResult
				if result.PromiseValue != nil {
					plain.Value = map[string]any{}
				}
				actual.MethodCalls = append(actual.MethodCalls, map[string]any{"name": name, "method": method, "arguments": args, "result": recordedResult(plain, true)})
			}
			producer.next = func(next *cooperative.Scope, request SyncNextRequest) (SyncIteratorResult[any], error) {
				event(name + ":next")
				result := SyncIteratorResult[any]{IteratorResult: IteratorResult[any]{Done: true}}
				if !finished {
					if !begun {
						begun = true
						if isActual {
							message, err := sdkoutput.Decode(original.Message)
							if err != nil {
								return result, err
							}
							if err := normalizer.NormalizeMessage(message, func(view *sdkoutput.View) error { views = append(views, view); return nil }); err != nil {
								return result, err
							}
						} else {
							event("inner:start")
						}
					}
					if isActual {
						if index < len(views) {
							result = SyncIteratorResult[any]{IteratorResult: IteratorResult[any]{Defined: true, Value: views[index]}}
							index++
						} else {
							finished = true
						}
					} else if index == 0 {
						index++
						result = SyncIteratorResult[any]{IteratorResult: IteratorResult[any]{Defined: true, Value: "one"}}
						if profile == "language-sync-yield-rejection" {
							value, _ := NewOwnerFuture[IteratorValue[any]](owner)
							_ = value.Reject(next, errors.New("original-failure"))
							result.PromiseValue = value
						}
					} else {
						event("inner:done")
						event("inner:finally")
						finished = true
						if profile == "language-sync-return" {
							result.Defined = true
							result.Value = "final"
						}
						if profile == "language-sync-return-promise" {
							result.Defined = true
							value, _ := NewOwnerFuture[IteratorValue[any]](owner)
							_ = value.Resolve(next, IteratorValue[any]{Defined: true, Value: "final"})
							result.PromiseValue = value
						}
					}
				}
				args := []any{}
				if request.ArgumentPresent {
					args = append(args, nil)
				}
				recordMethod("next", args, result)
				event(name + ":next-returned")
				return result, nil
			}
			producer.returning = func(_ *cooperative.Scope, request SyncReturnRequest[any]) (SyncIteratorResult[any], error) {
				event(name + ":return")
				if begun && !finished && !isActual {
					event("inner:finally")
				}
				finished = true
				result := SyncIteratorResult[any]{IteratorResult: IteratorResult[any]{Done: true, Defined: request.Defined, Value: request.Value}}
				args := []any{}
				if request.ArgumentPresent {
					args = append(args, request.Value)
				}
				recordMethod("return", args, result)
				event(name + ":return-returned")
				return result, nil
			}
			adapter, err := NewAsyncFromSync[any](owner, producer)
			if err != nil {
				return err
			}
			if profile == "actual-filehistory-normalizer" || profile == "actual-filehistory-return" {
				query, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, it *OwnerIterator[any], _ IteratorRequest[any]) error {
					event("query:start")
					return it.Yield(next, original.Message, func(later *cooperative.Scope, it *OwnerIterator[any], request IteratorRequest[any]) error {
						if !request.Returning {
							event("query:after-yield")
						}
						event("query:finally")
						if request.Returning {
							return it.CompleteReturn(later)
						}
						return it.Complete(later)
					})
				})
				completion := func(next *cooperative.Scope) *OwnerFuture[struct{}] {
					event("checkpoint:entry")
					result, _ := NewOwnerFuture[struct{}](owner)
					literal, _ := NewOwnerFuture[struct{}](owner)
					_ = literal.Resolve(next, struct{}{})
					_, _ = literal.Then(next, func(later *cooperative.Scope, _ struct{}, _ error) error {
						event("checkpoint:completed")
						return result.Resolve(later, struct{}{})
					})
					return result
				}
				wrapped, _ := NewOwnerIterator(owner, func(next *cooperative.Scope, it *OwnerIterator[any], request IteratorRequest[any]) error {
					return it.Delegate(next, query, request, func(later *cooperative.Scope, _ IteratorResult[any], pending IteratorRequest[any]) error {
						return AwaitGenerator(later, it, completion(later), func(done *cooperative.Scope, _ struct{}) error {
							if pending.Returning {
								return it.CompleteReturn(done)
							}
							return it.Complete(done)
						}, nil)
					}, nil)
				})
				var consume func(*cooperative.Scope, *OwnerIterator[any]) error
				consume = func(next *cooperative.Scope, it *OwnerIterator[any]) error {
					demand, err := wrapped.Next(next)
					if err != nil {
						return err
					}
					return AwaitGenerator(next, it, demand, func(later *cooperative.Scope, result IteratorResult[any]) error {
						if result.Done {
							event("ask:after-query")
							event("ask:finally")
							return it.Complete(later)
						}
						event("ask:message")
						return it.Delegate(later, adapter, IteratorRequest[any]{}, func(done *cooperative.Scope, _ IteratorResult[any], pending IteratorRequest[any]) error {
							if pending.Returning {
								closing, err := wrapped.ReturnUndefined(done)
								if err != nil {
									return err
								}
								return AwaitGenerator(done, it, closing, func(closed *cooperative.Scope, _ IteratorResult[any]) error {
									event("ask:finally")
									return it.CompleteReturn(closed)
								}, nil)
							}
							event("ask:after-normalize")
							return consume(done, it)
						}, nil)
					}, nil)
				}
				iterator, _ = NewOwnerIterator(owner, func(next *cooperative.Scope, it *OwnerIterator[any], _ IteratorRequest[any]) error {
					return consume(next, it)
				})
			} else {
				outerName := "outer"
				if isActual {
					outerName = "ask"
				}
				iterator, _ = NewOwnerIterator(owner, func(next *cooperative.Scope, it *OwnerIterator[any], request IteratorRequest[any]) error {
					event(outerName + ":start")
					return it.Delegate(next, adapter, request, func(later *cooperative.Scope, result IteratorResult[any], pending IteratorRequest[any]) error {
						if !pending.Returning {
							if isActual {
								event("ask:after-normalize")
							} else {
								value := "undefined"
								if result.Defined {
									value = fmt.Sprint(result.Value)
								}
								event("outer:after:" + value)
							}
						}
						event(outerName + ":finally")
						if pending.Returning {
							return it.CompleteReturn(later)
						}
						return it.Complete(later)
					}, func(later *cooperative.Scope, failure error) error {
						event(outerName + ":finally")
						return it.Fail(later, failure)
					})
				})
			}
		}
		for index := 0; ; index++ {
			event(fmt.Sprintf("caller:next:%d", index))
			var demand *OwnerFuture[IteratorResult[any]]
			var err error
			if index == 1 && (profile == "actual-normalizer-return" || profile == "actual-filehistory-return") {
				demand, err = iterator.Return(scope, "return-value")
			} else {
				demand, err = iterator.Next(scope)
			}
			if err != nil {
				return err
			}
			event(fmt.Sprintf("caller:returned:%d", index))
			_, _ = owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { event(fmt.Sprintf("independent:%d", index)); return nil })
			result, failure := demand.Await(scope)
			if failure != nil {
				event("consumer:error:" + failure.Error())
				done, _ := iterator.Next(scope)
				result, failure = done.Await(scope)
				if failure != nil {
					return failure
				}
				actual.IteratorResults = append(actual.IteratorResults, recordedResult(result, true))
				break
			}
			event(fmt.Sprintf("consumer:%d", index))
			if profile == "async-delegate-fallthrough" {
				actual.Results = append(actual.Results, recordedResult(result, false))
			} else {
				actual.IteratorResults = append(actual.IteratorResults, recordedResult(result, true))
			}
			if result.Done {
				break
			}
			if index > 8 {
				return errors.New("bounded original program exceeded")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return actual
}

func TestSyncIteratorActualTwelveSourcePrograms(t *testing.T) {
	data, err := os.ReadFile("testdata/sync_iterator_source.json")
	if err != nil {
		t.Fatal(err)
	}
	var originals []json.RawMessage
	if err = json.Unmarshal(data, &originals); err != nil {
		t.Fatal(err)
	}
	for _, expected := range originals {
		var header struct {
			Input   json.RawMessage `json:"input"`
			Profile string
		}
		_ = json.Unmarshal(expected, &header)
		var input struct{ Profile string }
		_ = json.Unmarshal(header.Input, &input)
		t.Run(input.Profile, func(t *testing.T) {
			actual := syncProgram(t, header.Input)
			raw, err := json.Marshal(actual)
			if err != nil {
				t.Fatal(err)
			}
			var got, want any
			if json.Unmarshal(raw, &got) != nil || json.Unmarshal(expected, &want) != nil {
				t.Fatal("invalid raw observation")
			}
			// Absent optional empty collections are restored only in observer projection;
			// original SDK wrapper/result values and phase arrays are compared in full.
			if _, ok := want.(map[string]any)["methodCalls"]; ok && got.(map[string]any)["methodCalls"] == nil {
				got.(map[string]any)["methodCalls"] = []any{}
			}
			if _, ok := want.(map[string]any)["iteratorResults"]; ok && got.(map[string]any)["iteratorResults"] == nil {
				got.(map[string]any)["iteratorResults"] = []any{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("actual %s\nSource %s", raw, expected)
			}
			if len(header.Input) > 0 && bytes.Contains(header.Input, []byte(`"message"`)) {
				// SDK wrapper bytes remain opaque model/history envelopes. Unlike observer
				// bookkeeping maps, their raw property order is part of this finite proof.
				var source, observed struct {
					MethodCalls []struct {
						Result struct{ Value json.RawMessage }
					} `json:"methodCalls"`
				}
				_ = json.Unmarshal(expected, &source)
				_ = json.Unmarshal(raw, &observed)
				for index, method := range source.MethodCalls {
					if len(method.Result.Value) == 0 {
						continue
					}
					var sourceBytes, observedBytes bytes.Buffer
					if json.Compact(&sourceBytes, method.Result.Value) != nil || json.Compact(&observedBytes, observed.MethodCalls[index].Result.Value) != nil {
						t.Fatal("invalid opaque SDK wrapper")
					}
					if !bytes.Equal(sourceBytes.Bytes(), observedBytes.Bytes()) {
						t.Fatalf("SDK wrapper raw bytes changed\nactual %s\nSource %s", observedBytes.Bytes(), sourceBytes.Bytes())
					}
				}
			}
		})
	}
}
