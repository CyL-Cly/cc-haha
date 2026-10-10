package sessioncontroller

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"testing"
)

func TestDetachedRecordObserverUsesOwnerReactionAndOriginalReason(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	original := errors.New("original Record failure")
	for _, late := range []bool{false, true} {
		future := &RecordFuture{future: newWriteFuture(), owner: owner}
		if late {
			future.future.settle(original)
		}
		called := 0
		done := make(chan struct{})
		e := owner.Run(context.Background(), func(s *cooperative.Scope) error {
			e := future.ObserveDetached(s, func(completed *cooperative.Scope, reason error) {
				if !completed.BelongsTo(owner) || reason != original {
					t.Error("error/owner identity changed")
				}
				called++
				close(done)
			})
			if e != nil {
				return e
			}
			if called != 0 {
				return errors.New("registration ran future callback inline")
			}
			if !late {
				future.future.settle(original)
			}
			return s.AwaitPromise(done)
		})
		if e != nil || called != 1 {
			t.Fatal(e, called)
		}
	}
}

func TestDetachedRecordObserverRejectsForeignOrMissingInputs(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	other := cooperative.New()
	defer other.Close()
	future := &RecordFuture{future: newWriteFuture(), owner: owner}
	if future.ObserveDetached(nil, func(*cooperative.Scope, error) {}) != cooperative.ErrRestricted {
		t.Fatal("nil scope")
	}
	if (*RecordFuture)(nil).ObserveDetached(nil, nil) != cooperative.ErrRestricted {
		t.Fatal("nil future")
	}
	if (&RecordFuture{}).ObserveDetached(nil, nil) != cooperative.ErrRestricted {
		t.Fatal("uninitialized future")
	}
	e := other.Run(context.Background(), func(s *cooperative.Scope) error { return future.ObserveDetached(s, func(*cooperative.Scope, error) {}) })
	if e != cooperative.ErrRestricted {
		t.Fatal(e)
	}
	e = owner.Run(context.Background(), func(s *cooperative.Scope) error { return future.ObserveDetached(s, nil) })
	if e != cooperative.ErrRestricted {
		t.Fatal(e)
	}
}
