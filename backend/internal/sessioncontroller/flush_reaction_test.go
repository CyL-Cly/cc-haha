package sessioncontroller

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"runtime"
	"testing"
	"time"
)

func TestFlushReactionAlwaysRunsAfterSynchronousStartOnOwner(t *testing.T) {
	for _, failure := range []error{nil, errors.New("original physical flush rejection")} {
		owner := cooperative.New()
		defer owner.Close()
		parent := &FlushFuture{future: newWriteFuture(), owner: owner}
		parent.future.settle(failure)
		var dependent *ownerasync.OwnerFuture[struct{}]
		entered := false
		pendingCall(t, owner, cooperative.Input, func(s *cooperative.Scope) error {
			var e error
			dependent, e = parent.Then(s, func(next *cooperative.Scope, reason error) error {
				if !next.BelongsTo(owner) || reason != failure {
					return errors.New("owner/error identity lost")
				}
				entered = true
				return reason
			})
			if entered {
				return errors.New("fulfilled promise reaction ran synchronously")
			}
			return e
		})
		pendingCall(t, owner, cooperative.Input, func(s *cooperative.Scope) error {
			_, e := dependent.Await(s)
			if e != failure {
				return errors.New("dependent reason changed")
			}
			return nil
		})
		if !entered {
			t.Fatal("reaction was not entered")
		}
	}
}
func TestFlushReactionRejectsOwnerAndCallbackAdmission(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	foreign := cooperative.New()
	defer foreign.Close()
	parent := &FlushFuture{future: newWriteFuture(), owner: owner}
	run := func(*cooperative.Scope, error) error { return nil }
	pendingCall(t, foreign, cooperative.Input, func(s *cooperative.Scope) error {
		f, e := parent.Then(s, run)
		if f != nil || e != cooperative.ErrRestricted {
			return errors.New("foreign flush reaction accepted")
		}
		return nil
	})
	for _, future := range []*FlushFuture{nil, {owner: owner}} {
		if f, e := future.Then(nil, run); f != nil || e != cooperative.ErrRestricted {
			t.Fatal("nil future admitted")
		}
	}
	pendingCall(t, owner, cooperative.Input, func(s *cooperative.Scope) error {
		f, e := parent.Then(s, nil)
		if f != nil || e != cooperative.ErrRestricted {
			return errors.New("missing reaction accepted")
		}
		return nil
	})
	var stale *cooperative.Scope
	pendingCall(t, owner, cooperative.Input, func(s *cooperative.Scope) error { stale = s; return nil })
	if f, e := parent.Then(stale, run); f != nil || e != cooperative.ErrRestricted {
		t.Fatal("expired scope admitted")
	}
}
func TestFlushReactionOwnerCloseSettlesDependentWithoutBusiness(t *testing.T) {
	owner := cooperative.New()
	parent := &FlushFuture{future: newWriteFuture(), owner: owner}
	var dependent *ownerasync.OwnerFuture[struct{}]
	business := false
	pendingCall(t, owner, cooperative.Input, func(s *cooperative.Scope) error {
		var e error
		dependent, e = parent.Then(s, func(*cooperative.Scope, error) error { business = true; return nil })
		return e
	})
	owner.Close()
	_, e := dependent.Wait(context.Background())
	if e != cooperative.ErrClosed || business {
		t.Fatalf("close lost obligation or ran metadata: %v %v", e, business)
	}
	parent.future.settle(nil)
	_, e = dependent.Wait(context.Background())
	if e != cooperative.ErrClosed || business {
		t.Fatal("late physical settlement resurrected cleanup")
	}
}
func TestFlushReactionCancelledRegistrationDoesNotAllocateDependent(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	parent := &FlushFuture{future: newWriteFuture(), owner: owner}
	cause := errors.New("caller administrative cancellation")
	ctx, cancel := context.WithCancelCause(context.Background())
	err := owner.Run(ctx, func(s *cooperative.Scope) error {
		cancel(cause)
		future, e := parent.Then(s, func(*cooperative.Scope, error) error { return nil })
		if future != nil || e != cause {
			return errors.New("cancelled registration created a dependent")
		}
		return nil
	})
	if err != nil && err != cause {
		t.Fatal(err)
	}
}

func TestFlushReactionConcurrentOwnerCloseSettlesEnteredCallback(t *testing.T) {
	for _, failure := range []error{nil, errors.New("original callback failure during administrative close")} {
		owner := cooperative.New()
		parent := &FlushFuture{future: newWriteFuture(), owner: owner}
		entered := make(chan struct{})
		var dependent *ownerasync.OwnerFuture[struct{}]
		pendingCall(t, owner, cooperative.Input, func(s *cooperative.Scope) error {
			var err error
			dependent, err = parent.Then(s, func(next *cooperative.Scope, _ error) error {
				close(entered)
				// Check the actual owner-life cancellation, not a predetermined await
				// count or delay. Register performs only administrative scope validation.
				deadline := time.Now().Add(5 * time.Second)
				for {
					err := next.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
					if err == cooperative.ErrClosed {
						return failure
					}
					if err != nil {
						return err
					}
					if time.Now().After(deadline) {
						return errors.New("owner close did not cancel entered callback")
					}
					runtime.Gosched()
				}
			})
			return err
		})
		parent.future.settle(nil)
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			owner.Close()
			t.Fatal("callback not entered")
		}
		closed := make(chan struct{})
		go func() { owner.Close(); close(closed) }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := dependent.Wait(ctx)
		cancel()
		expected := failure
		if expected == nil {
			expected = cooperative.ErrClosed
		}
		<-closed
		if err != expected {
			t.Fatalf("entered callback obligation/reason was stranded: %v want %v", err, expected)
		}
	}
}
