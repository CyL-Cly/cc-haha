package sdkhost_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func rawSDKInput(owner *cooperative.Owner, inputs []protocol.JSONObject) (*sdkhost.PlainStructuredInput, error) {
	index := 0
	var step sdkhost.GeneratorContinuation[string]
	step = func(scope *cooperative.Scope, iterator *sdkhost.OwnerIterator[string], request sdkhost.IteratorRequest[string]) error {
		if request.Returning {
			return iterator.CompleteReturn(scope)
		}
		if index == len(inputs) {
			return iterator.Complete(scope)
		}
		value := source(inputs[index]) + "\n"
		index++
		return iterator.Yield(scope, value, step)
	}
	raw, err := sdkhost.NewOwnerIterator(owner, step)
	if err != nil {
		return nil, err
	}
	return sdkhost.NewPlainStructuredInput(owner, raw)
}

func countPhase(result map[string]any, phase string) int {
	count := 0
	for _, value := range result["phases"].([]string) {
		if value == phase {
			count++
		}
	}
	return count
}

func TestRunStreamWrapperSetupFailureFinalizesAcceptedASK(t *testing.T) {
	for _, name := range []string{"error", "nil", "foreign-owner", "completed-before-start", "failed-before-start"} {
		t.Run(name, func(t *testing.T) {
			stdout, result := originalBoundary(t, nativeControls{Run: func(options sdkhost.Options, host *sdkhost.Host, inputs []protocol.JSONObject) error {
				other := cooperative.New()
				defer other.Close()
				return options.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					input, err := rawSDKInput(options.Owner, inputs[:1])
					if err != nil {
						return err
					}
					err = host.RunStream(scope, context.Background(), input, sdkhost.StreamOptions{WrapASK: func(inner *sdkhost.OwnerIterator[[]byte]) (*sdkhost.OwnerIterator[[]byte], error) {
						switch name {
						case "error":
							return nil, errors.New("original observational wrapper constructor failed")
						case "nil":
							return nil, nil
						case "completed-before-start", "failed-before-start":
							return sdkhost.NewOwnerIterator(options.Owner, func(scope *cooperative.Scope, iterator *sdkhost.OwnerIterator[[]byte], _ sdkhost.IteratorRequest[[]byte]) error {
								if name == "failed-before-start" {
									return iterator.Fail(scope, errors.New("original wrapper failed before start"))
								}
								return iterator.Complete(scope)
							})
						default:
							return sdkhost.NewOwnerIterator(other, func(scope *cooperative.Scope, iterator *sdkhost.OwnerIterator[[]byte], _ sdkhost.IteratorRequest[[]byte]) error {
								return iterator.Complete(scope)
							})
						}
					}})
					var capability *sdkhost.CapabilityError
					if name == "failed-before-start" {
						if err == nil || !strings.Contains(err.Error(), "original wrapper failed before start") {
							return fmt.Errorf("original failed wrapper result: %v", err)
						}
					} else if !errors.As(err, &capability) || !strings.Contains(err.Error(), "wrapper") {
						return fmt.Errorf("invalid wrapper result: %v", err)
					}
					// A failed wrapper must discharge the accepted turn, so the same
					// unique drain can inspect an empty input without overlapping ASK.
					empty, err := rawSDKInput(options.Owner, nil)
					if err != nil {
						return err
					}
					return host.RunStream(scope, context.Background(), empty, sdkhost.StreamOptions{})
				})
			}})
			// The original restored session publishes its cache once before ASK.
			if len(stdout) != 0 || len(result["requests"].([]string)) != 0 || countPhase(result, "ask:finally") != 1 || countPhase(result, "read-cache:return") != 2 {
				t.Fatalf("failed wrapper output=%d requests=%d finally=%d cache=%d", len(stdout), len(result["requests"].([]string)), countPhase(result, "ask:finally"), countPhase(result, "read-cache:return"))
			}
		})
	}
}

func TestRunStreamInitOutputFailureReturnsProducerAndFinalizes(t *testing.T) {
	stdout, result := originalBoundary(t, nativeControls{AllowFailure: true, Configure: func(options *sdkhost.Options) {
		options.Output.Enqueue = func([]byte) error { return errors.New("original stream FIFO failed") }
	}, Run: func(options sdkhost.Options, host *sdkhost.Host, inputs []protocol.JSONObject) error {
		return options.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			input, err := rawSDKInput(options.Owner, inputs[:1])
			if err != nil {
				return err
			}
			return host.RunStream(scope, context.Background(), input, sdkhost.StreamOptions{})
		})
	}})
	requireFailure(t, result, "original stream FIFO failed")
	if len(stdout) != 0 || len(result["requests"].([]string)) != 0 || countPhase(result, "ask:finally") != 1 || countPhase(result, "read-cache:return") != 2 {
		t.Fatalf("FIFO error output=%d requests=%d finally=%d cache=%d", len(stdout), len(result["requests"].([]string)), countPhase(result, "ask:finally"), countPhase(result, "read-cache:return"))
	}
}

func TestRunStreamWrapperPrefixOutputFailureWakesUnstartedProducer(t *testing.T) {
	_, result := originalBoundary(t, nativeControls{AllowFailure: true, Configure: func(options *sdkhost.Options) {
		options.Output.Enqueue = func([]byte) error { return errors.New("original wrapper prefix FIFO failed") }
	}, Run: func(options sdkhost.Options, host *sdkhost.Host, inputs []protocol.JSONObject) error {
		return options.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			input, err := rawSDKInput(options.Owner, inputs[:1])
			if err != nil {
				return err
			}
			return host.RunStream(scope, context.Background(), input, sdkhost.StreamOptions{WrapASK: func(inner *sdkhost.OwnerIterator[[]byte]) (*sdkhost.OwnerIterator[[]byte], error) {
				return sdkhost.NewOwnerIterator(options.Owner, func(scope *cooperative.Scope, iterator *sdkhost.OwnerIterator[[]byte], _ sdkhost.IteratorRequest[[]byte]) error {
					return iterator.Yield(scope, []byte(`{"type":"system","subtype":"observer-prefix"}`), func(scope *cooperative.Scope, iterator *sdkhost.OwnerIterator[[]byte], request sdkhost.IteratorRequest[[]byte]) error {
						if request.Returning {
							return iterator.CompleteReturn(scope)
						}
						return errors.New("unexpected observer prefix consumer resume")
					})
				})
			}})
		})
	}})
	requireFailure(t, result, "original wrapper prefix FIFO failed")
	if len(result["requests"].([]string)) != 0 || countPhase(result, "ask:finally") != 1 {
		t.Fatalf("prefix FIFO fault leaked accepted turn: %#v", result)
	}
}

func TestRunStreamActualTwoMachineInputAfterFirstResult(t *testing.T) {
	var current *cooperative.Scope
	var firstResult *sdkhost.OwnerFuture[struct{}]
	var firstResultSeen bool
	stdout, result := originalBoundary(t, nativeControls{Configure: func(options *sdkhost.Options) {
		enqueue := options.Output.Enqueue
		options.Output.Enqueue = func(data []byte) error {
			if err := enqueue(data); err != nil {
				return err
			}
			if object(data).String("type") == "result" && !firstResultSeen {
				firstResultSeen = true
				return firstResult.Resolve(current, struct{}{})
			}
			return nil
		}
	}, Run: func(options sdkhost.Options, host *sdkhost.Host, inputs []protocol.JSONObject) error {
		return options.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			current = scope
			var err error
			firstResult, err = sdkhost.NewOwnerFuture[struct{}](options.Owner)
			if err != nil {
				return err
			}
			raw, err := sdkhost.NewOwnerIterator(options.Owner, func(scope *cooperative.Scope, iterator *sdkhost.OwnerIterator[string], _ sdkhost.IteratorRequest[string]) error {
				return iterator.Yield(scope, source(inputs[0])+"\n", func(scope *cooperative.Scope, iterator *sdkhost.OwnerIterator[string], _ sdkhost.IteratorRequest[string]) error {
					return sdkhost.AwaitGenerator(scope, iterator, firstResult, func(scope *cooperative.Scope, _ struct{}) error {
						return iterator.Yield(scope, source(inputs[1])+"\n", func(scope *cooperative.Scope, iterator *sdkhost.OwnerIterator[string], _ sdkhost.IteratorRequest[string]) error {
							return iterator.Complete(scope)
						})
					}, nil)
				})
			})
			if err != nil {
				return err
			}
			input, err := sdkhost.NewPlainStructuredInput(options.Owner, raw)
			if err != nil {
				return err
			}
			return host.RunStream(scope, context.Background(), input, sdkhost.StreamOptions{})
		})
	}})
	if strings.Count(string(stdout), `"type":"result"`) != 2 || len(result["requests"].([]string)) != 2 || countPhase(result, "ask:finally") != 2 || countPhase(result, "read-cache:return") != 3 {
		t.Fatalf("actual sequence result=%d requests=%d finally=%d cache=%d", strings.Count(string(stdout), `"type":"result"`), len(result["requests"].([]string)), countPhase(result, "ask:finally"), countPhase(result, "read-cache:return"))
	}
}
