package sdkhost

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
)

func TestQueryIteratorBridgeOriginalFrameFinallyAndErrorOwnership(t *testing.T) {
	for _, name := range []string{"normal", "return", "query-throw", "finally-throw", "body-throw"} {
		t.Run(name, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			innerErr, finallyErr, bodyErr := errors.New("original query throw"), errors.New("original complete throw"), errors.New("original ASK body throw")
			count, checkpoints := 0, 0
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				bridge, err := newQueryIteratorBridge(scope, owner)
				if err != nil {
					return err
				}
				message := &queryloop.Message{}
				consumer := func(value queryloop.Yield) (requestretry.NoticeAction, error) {
					count++
					if count == 1 && !value.RequestStart {
						t.Fatal("filtered RequestStart bypassed original query iterator")
					}
					if count == 2 && value.Message != message {
						t.Fatal("original producer message identity was replaced")
					}
					// This is original Frame policy. It must never execute in Then.
					if err := scope.Frame(cooperative.FrameFeed, func() error { return nil }); err != nil {
						return 0, err
					}
					if name == "return" {
						return requestretry.Stop, nil
					}
					if name == "body-throw" {
						return 0, bodyErr
					}
					return requestretry.Resume, nil
				}
				action, emitErr := bridge.emit(scope, queryloop.Yield{RequestStart: true}, consumer)
				if name == "body-throw" && !errors.Is(emitErr, bodyErr) {
					t.Fatalf("body error changed before close: %v", emitErr)
				}
				if name != "body-throw" && emitErr != nil {
					return emitErr
				}
				if name == "normal" {
					if action != requestretry.Resume {
						t.Fatal("normal demand stopped producer")
					}
					if _, err = bridge.emit(scope, queryloop.Yield{Message: message}, consumer); err != nil {
						return err
					}
				}
				var queryErr error
				if name == "query-throw" || name == "finally-throw" {
					queryErr = innerErr
				}
				if name == "body-throw" {
					queryErr = bodyErr
				}
				finishErr := bridge.finish(scope, queryloop.Outcome{Reason: "completed"}, queryErr, func() error {
					checkpoints++
					if err := scope.Frame(cooperative.FrameFinish, func() error { return nil }); err != nil {
						return err
					}
					if err := scope.AwaitFulfilled(); err != nil {
						return err
					}
					if name == "finally-throw" || name == "body-throw" {
						return finallyErr
					}
					return nil
				})
				var want error
				switch name {
				case "query-throw":
					want = innerErr
				case "finally-throw":
					want = finallyErr
				case "body-throw":
					want = bodyErr
				}
				if finishErr != want {
					t.Fatalf("original finally ownership: got=%v want=%v", finishErr, want)
				}
				if name == "normal" {
					result, err := bridge.next.Wait(context.Background())
					if err != nil || !result.Done || result.Defined {
						t.Fatal("fileHistory exposed unused terminal", result, err)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			wantCount := 1
			if name == "normal" {
				wantCount = 2
			}
			if count != wantCount || checkpoints != 1 {
				t.Fatalf("body=%d checkpoint=%d", count, checkpoints)
			}
		})
	}
}

// Constructor checking obtains the real provider administrative marker from
// the original cancelled Scope. The retry wrapper remains a finite component
// input here; native HTTP/retry creation is separately tested in modelyield.
type queryFailedBoundary struct {
	provider.NativeOwnerBoundary
	scope *cooperative.Scope
}

func (boundary queryFailedBoundary) CheckOwner() error {
	return boundary.scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
}

func TestQueryIteratorAdministrativeFailureKeepsOriginalRetryType(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	cause := errors.New("original caller Scope stopped")
	err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		bridge, err := newQueryIteratorBridge(scope, owner)
		if err != nil {
			return err
		}
		cancel(cause)
		_, failure := provider.NewOwnerNativeIO(queryFailedBoundary{scope: scope}, nil)
		if !provider.IsOwnerWaitFailure(failure) || !errors.Is(failure, cause) {
			return fmt.Errorf("provider did not establish administrative error: %v", failure)
		}
		original := &retrypolicy.CannotRetryError{Original: failure}
		checkpoints := 0
		got := bridge.finish(scope, queryloop.Outcome{}, original, func() error { checkpoints++; return nil })
		if got != original || !provider.IsOwnerWaitFailure(got) || checkpoints != 0 {
			return fmt.Errorf("handoff masked original retry wrapper: got=%T %v checkpoints=%d", got, got, checkpoints)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestQueryIteratorReturnDoesNotHideIndependentProducerFailure(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	original := errors.New("producer cleanup failed after stop")
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		bridge, err := newQueryIteratorBridge(scope, owner)
		if err != nil {
			return err
		}
		if _, err = bridge.emit(scope, queryloop.Yield{RequestStart: true}, func(queryloop.Yield) (requestretry.NoticeAction, error) { return requestretry.Stop, nil }); err != nil {
			return err
		}
		count := 0
		got := bridge.finish(scope, queryloop.Outcome{}, original, func() error { count++; return nil })
		if got != original || count != 1 {
			return fmt.Errorf("return swallowed producer failure: got=%v checkpoint=%d", got, count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestQueryIteratorCancelledCheckpointHandoffPreservesActualThrow(t *testing.T) {
	for _, bodyThrows := range []bool{false, true} {
		t.Run(map[bool]string{false: "finally", true: "body"}[bodyThrows], func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			bodyErr, finallyErr := errors.New("actual ASK body throw"), errors.New("actual checkpoint throw")
			if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
				bridge, err := newQueryIteratorBridge(scope, owner)
				if err != nil {
					return err
				}
				var queryErr error
				if bodyThrows {
					_, queryErr = bridge.emit(scope, queryloop.Yield{RequestStart: true}, func(queryloop.Yield) (requestretry.NoticeAction, error) { return 0, bodyErr })
					if queryErr != bodyErr {
						return fmt.Errorf("body throw changed: %v", queryErr)
					}
				}
				got := bridge.finish(scope, queryloop.Outcome{}, queryErr, func() error { cancel(errors.New("checkpoint receipt handoff stopped")); return finallyErr })
				want := finallyErr
				if bodyThrows {
					want = bodyErr
				}
				if got != want {
					return fmt.Errorf("handoff replaced actual throw: got=%v want=%v", got, want)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQueryIteratorConsumerAwaitsActualNextReaction(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		bridge, err := newQueryIteratorBridge(scope, owner)
		if err != nil {
			return err
		}
		observed := false
		_, err = bridge.next.Then(scope, func(_ *cooperative.Scope, _ IteratorResult[queryValue], err error) error {
			if err != nil {
				return err
			}
			observed = true
			return nil
		})
		if err != nil {
			return err
		}
		_, emitErr := bridge.emit(scope, queryloop.Yield{RequestStart: true}, func(queryloop.Yield) (requestretry.NoticeAction, error) {
			if !observed {
				return 0, fmt.Errorf("consumer bypassed next Promise reaction")
			}
			return requestretry.Resume, nil
		})
		return bridge.finish(scope, queryloop.Outcome{}, emitErr, func() error { return nil })
	}); err != nil {
		t.Fatal(err)
	}
}
