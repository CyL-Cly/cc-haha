package gitbranch

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// This is a caller/Owner reaction regression, not a substitute for native
// StatWatcher readiness or a Source phase oracle. The producer settles a real
// Service future after admitting an ordinary callback. A fulfilled Promise
// consumer must resume its original stack before that ordinary callback.
func TestParallelCallerResumesPromiseOnOriginalFrame(t *testing.T) {
	h := newHarness(t, nil)
	trace := []string{}
	callbackDone := make(chan struct{})
	operations := []fixtureGitAwaitOperation{
		{start: func(scope *cooperative.Scope) (*Future, error) {
			future := h.s.future()
			_, err := h.owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error {
				trace = append(trace, "aggregate-settlement")
				_, err := h.owner.Submit(context.Background(), cooperative.Read, func(*cooperative.Scope) error {
					trace = append(trace, "ordinary-callback")
					close(callbackDone)
					return nil
				})
				if err != nil {
					return err
				}
				h.s.settle(next, future, result{raw: raw([]string{"main", "main"})})
				return nil
			})
			return future, err
		}, finish: func(json.RawMessage) {}},
		{start: func(scope *cooperative.Scope) (*Future, error) {
			trace = append(trace, "following-original-call")
			future := h.s.future()
			h.s.settle(scope, future, result{raw: raw("main")})
			return future, nil
		}, finish: func(json.RawMessage) {}},
	}
	var err error
	if os.Getenv("CC_HAHA_GIT_PARALLEL_CALLER_LEGACY") == "1" {
		// Mechanical old topology: an administrative off-owner Wait followed by
		// a new Owner entry. No values/clock/cache are altered for this control.
		var first *Future
		receipt, startErr := h.owner.Submit(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
			var err error
			first, err = operations[0].start(scope)
			return err
		})
		if startErr == nil {
			startErr = receipt.Wait()
		}
		if startErr == nil {
			_, startErr = first.Wait(context.Background())
		}
		if startErr == nil {
			startErr = runFixtureGitAwaitChain(h.owner, operations[1:])
		}
		err = startErr
	} else {
		err = runFixtureGitAwaitChain(h.owner, operations)
	}
	if err != nil {
		t.Fatal(err)
	}
	// Only after the original program ends do we wait for this test's callback
	// observation; it is not an extra business await or a general task drain.
	<-callbackDone
	want := []string{"aggregate-settlement", "following-original-call", "ordinary-callback"}
	if !reflect.DeepEqual(trace, want) {
		t.Fatalf("original fulfilled-Promise caller was split into a new Owner entry: want %v; got %v", want, trace)
	}
}
