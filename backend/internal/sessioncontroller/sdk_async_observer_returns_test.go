package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

func bindAsyncSDKObserverTest(t *testing.T, owner *cooperative.Owner, controller *Controller) {
	t.Helper()
	if controller.options.OnSDKUUIDLookup == nil {
		controller.options.OnSDKUUIDLookup = func(json.RawMessage, bool) {}
	}
	if controller.options.OnSDKRecord == nil {
		controller.options.OnSDKRecord = func() {}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := owner.Run(ctx, controller.BindSDKAsyncReturnObservers); err != nil {
		t.Fatal(err)
	}
}

// Observe the real cache Promise and actual SDK admission, rather than using a
// synthetic await count. Only the explicitly bound Source spy has an outer
// lookup Promise; ordinary synchronous observers retain their previous order.
func TestSDKAsyncLookupObserverUsesOriginalResultReturnBoundary(t *testing.T) {
	for _, asynchronous := range []bool{false, true} {
		t.Run(fmt.Sprint(asynchronous), func(t *testing.T) {
			owner, controller, serial := parsedSDKHost(t)
			writePrivate(t, controller.writer.target(), uuidOld)
			var events []string
			controller.options.OnSDKUUIDLookup = func(id json.RawMessage, historical bool) {
				events = append(events, fmt.Sprintf("lookup:%s:%t", id, historical))
			}
			controller.options.OnSDKRecord = func() {}
			if asynchronous {
				bindAsyncSDKObserverTest(t, owner, controller)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
				cache, err := serial.pending.SessionUUIDs(scope, controller.options.ResumeOptions.Loader, controller.SessionID())
				if err != nil {
					return err
				}
				if err = cache.future.Await(scope); err != nil {
					return err
				}
				input, err := serial.AdmitParsedSDK(scope, parsedInput("actual-observed-user", "unchanged accepted user"))
				if err != nil {
					return err
				}
				if err = cache.future.then(scope, func(*cooperative.Scope, error) { events = append(events, "actual-cache:fulfilled") }); err != nil {
					return err
				}
				admission, err := input.Await(scope)
				if err != nil || admission.Duplicate || admission.Historical || len(controller.queue) != 1 || string(controller.queue[0].UUID) != `"actual-observed-user"` {
					return fmt.Errorf("actual admission changed: %+v %v", admission, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			want := []string{`lookup:"actual-observed-user":false`, "actual-cache:fulfilled"}
			if asynchronous {
				want[0], want[1] = want[1], want[0]
			}
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("original lookup versus observer async return: actual=%v expected=%v", events, want)
			}
		})
	}
}

// Both results come from actual Record futures. Empty input is the original
// async Record's fulfilled-null branch; null content is its original TypeError.
// The observer cannot replace either UUID or error identity.
func TestSDKAsyncRecordObserverAdoptsActualReturnAndPreservesError(t *testing.T) {
	owner, controller, serial := parsedSDKHost(t)
	bindAsyncSDKObserverTest(t, owner, controller)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		original, err := serial.pending.BeginRecordLive(scope, controller.options.ResumeOptions.Loader, LiveRecordProfile{QuerySource: "sdk"}, nil, RecordOptions{PromptID: func() json.RawMessage { return nil }})
		if err != nil {
			return err
		}
		returned, err := controller.sdkAsyncObservers.recordReturn(scope, original)
		if err != nil || returned == original {
			return fmt.Errorf("distinct observer Record return: %v", err)
		}
		pendingAtOriginal := false
		if err = original.future.then(scope, func(*cooperative.Scope, error) {
			select {
			case <-returned.Done():
			default:
				pendingAtOriginal = true
			}
		}); err != nil {
			return err
		}
		value, err := returned.Await(scope)
		if err != nil || string(value) != "null" || !pendingAtOriginal {
			return fmt.Errorf("original Record adoption: value=%s pendingAtOriginal=%v err=%v", value, pendingAtOriginal, err)
		}
		bad := toolRecordObject(t, `{"type":"user","uuid":"observer-null-block","message":{"role":"user","content":[null]}}`)
		rejected, err := serial.pending.BeginRecordLive(scope, controller.options.ResumeOptions.Loader, LiveRecordProfile{QuerySource: "sdk"}, []LiveRecord{{Record: bad}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
		if err != nil {
			return err
		}
		forwarded, err := controller.sdkAsyncObservers.recordReturn(scope, rejected)
		if err != nil {
			return err
		}
		_, originalError := rejected.Await(scope)
		_, observerError := forwarded.Await(scope)
		var loggingError *LoggingTypeError
		if !errors.As(originalError, &loggingError) || observerError != originalError {
			return fmt.Errorf("observer changed original rejection identity: %T/%v %T/%v", originalError, originalError, observerError, observerError)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSDKAsyncFlushObserverKeepsNilIdentityAndRealReturn(t *testing.T) {
	owner, controller, _ := parsedSDKHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		actual, err := ownerasync.NewOwnerFuture[struct{}](owner)
		if err != nil {
			return err
		}
		unchanged, err := controller.WrapSDKFlushObserverReturn(scope, actual)
		if err != nil || unchanged != actual {
			return errors.New("nil observer changed original export identity")
		}
		controller.options.OnSDKUUIDLookup = func(json.RawMessage, bool) {}
		controller.options.OnSDKRecord = func() {}
		if err = controller.BindSDKAsyncReturnObservers(scope); err != nil {
			return err
		}
		returned, err := controller.WrapSDKFlushObserverReturn(scope, actual)
		if err != nil || returned == actual {
			return fmt.Errorf("observer export not distinct: %v", err)
		}
		failure := errors.New("original export failure")
		if err = actual.Reject(scope, failure); err != nil {
			return err
		}
		pendingAtOriginal := false
		if _, err = actual.Then(scope, func(*cooperative.Scope, struct{}, error) error {
			select {
			case <-returned.Done():
			default:
				pendingAtOriginal = true
			}
			return nil
		}); err != nil {
			return err
		}
		_, observed := returned.Await(scope)
		if observed != failure || !pendingAtOriginal {
			return fmt.Errorf("observer export changed return/error: %v pending=%v", observed, pendingAtOriginal)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSDKAsyncObserverBindingRejectsMissingForeignRetainedAndClosed(t *testing.T) {
	owner, controller, _ := parsedSDKHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		if !errors.Is(controller.BindSDKAsyncReturnObservers(scope), cooperative.ErrRestricted) {
			return errors.New("missing observation callbacks admitted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bindAsyncSDKObserverTest(t, owner, controller)
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if !errors.Is(controller.BindSDKAsyncReturnObservers(scope), cooperative.ErrRestricted) {
			return errors.New("foreign owner admitted observer binding")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	foreignFuture, err := ownerasync.NewOwnerFuture[struct{}](foreign)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		if _, err := controller.WrapSDKFlushObserverReturn(scope, foreignFuture); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign returned Promise admitted: %v", err)
		}
		forged := &RecordFuture{future: newWriteFuture(), owner: foreign}
		if _, err := controller.sdkAsyncObservers.recordReturn(scope, forged); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign Record return admitted: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var retained *cooperative.Scope
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error { retained = scope; return nil }); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(controller.BindSDKAsyncReturnObservers(retained), cooperative.ErrRestricted) {
		t.Fatal("retained scope admitted observer binding")
	}
	owner.Close()
	if _, err := controller.sdkAsyncObserverBinding(retained); !errors.Is(err, cooperative.ErrRestricted) && !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("closed owner admitted observer binding", err)
	}
}
