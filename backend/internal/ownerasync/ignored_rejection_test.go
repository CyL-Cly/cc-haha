package ownerasync

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"testing"
)

func TestIgnoredDependentActualIdentityOriginalErrorAndTwoSubscriptions(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	tracker, _ := NewIgnoredRejections(owner)
	cause := errors.New("original promise rejection")
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		p, _ := NewOwnerFuture[int](owner)
		_ = p.Reject(scope, cause)
		children := []*OwnerFuture[struct{}]{}
		for _, site := range []string{"query", "success"} {
			child, e := p.Then(scope, func(_ *cooperative.Scope, _ int, failure error) error { return failure })
			if e != nil {
				return e
			}
			if child.Identity() == p.Identity() {
				t.Fatal("dependent reused holder")
			}
			if e = Ignore(scope, child, tracker, p.Identity(), site); e != nil {
				return e
			}
			if e = Ignore(scope, child, tracker, p.Identity(), site); e != nil {
				return e
			}
			children = append(children, child)
		}
		before, _ := tracker.Pending(scope)
		if len(before) != 0 {
			t.Fatal("reaction executed inline")
		}
		_, _ = p.Await(scope)
		after, _ := tracker.Pending(scope)
		if len(after) != 2 || after[0].Original != cause || after[1].Original != cause || after[0].Promise == after[1].Promise {
			t.Fatalf("lost original rejection/identities %+v", after)
		}
		if e := tracker.RuntimeCheckpoint(scope); e == nil {
			t.Fatal("invented native event turn")
		}
		_, _ = children[0].Await(scope)
		after, _ = tracker.Pending(scope)
		if len(after) != 1 || after[0].Site != "success" {
			t.Fatal("handled dependent still pending")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestIgnoredCallbackFailureLateTrackAndAdministration(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	tracker, _ := NewIgnoredRejections(owner)
	cause := errors.New("original callback throw")
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		p, _ := NewOwnerFuture[int](owner)
		_ = p.Resolve(scope, 1)
		child, _ := p.Then(scope, func(*cooperative.Scope, int, error) error { return cause })
		_, _ = p.Await(scope)
		if e := Ignore(scope, child, tracker, p.Identity(), "query"); e != nil {
			return e
		}
		notices, _ := tracker.Pending(scope)
		if len(notices) != 1 || notices[0].Original != cause {
			t.Fatal("late ignored registration lost original settlement")
		}
		admin, _ := NewOwnerFuture[int](owner)
		_ = Ignore(scope, admin, tracker, p.Identity(), "administrative")
		_ = admin.RejectAdministrative(cause)
		later, _ := tracker.Pending(scope)
		if len(later) != 1 {
			t.Fatal("administrative reason dispatched as native rejection")
		}
		fulfilled, _ := NewOwnerFuture[int](owner)
		_ = Ignore(scope, fulfilled, tracker, p.Identity(), "fulfilled")
		_ = fulfilled.Resolve(scope, 2)
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestIgnoredScopeAndCloseGuards(t *testing.T) {
	owner := cooperative.New()
	other := cooperative.New()
	defer other.Close()
	tracker, _ := NewIgnoredRejections(owner)
	p, _ := NewOwnerFuture[int](owner)
	holder, _ := NewOwnerFuture[int](owner)
	if _, e := NewIgnoredRejections(nil); e == nil {
		t.Fatal("nil owner")
	}
	if tracker.Owns(other) {
		t.Fatal("foreign tracker")
	}
	if e := other.Run(context.Background(), func(scope *cooperative.Scope) error {
		if e := Ignore(scope, p, tracker, holder.Identity(), "query"); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal("foreign admission")
		}
		if _, e := tracker.Pending(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal("foreign read")
		}
		if e := tracker.RuntimeCheckpoint(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal("foreign checkpoint")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, try := range []func() error{func() error { return Ignore[int](scope, nil, tracker, holder.Identity(), "query") }, func() error { return Ignore(scope, p, nil, holder.Identity(), "query") }, func() error { return Ignore(scope, p, tracker, p.Identity(), "query") }, func() error { return Ignore(scope, p, tracker, nil, "query") }, func() error { return Ignore(scope, p, tracker, holder.Identity(), "") }} {
			if try() == nil {
				t.Fatal("invalid ignored input")
			}
		}
		if e := Ignore(scope, p, tracker, holder.Identity(), "query"); e != nil {
			return e
		}
		if e := Ignore(scope, p, tracker, holder.Identity(), "other"); e == nil {
			t.Fatal("changed ignored registration")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	owner.Close()
	_, e := p.Wait(context.Background())
	if !errors.Is(e, cooperative.ErrClosed) {
		t.Fatal(e)
	}
	var nilFuture *OwnerFuture[int]
	if nilFuture.Identity() != nil {
		t.Fatal("nil future identity")
	}
}

// The observation occurs outside Owner.Run, so a missing late registration is
// a normal assertion failure rather than terminating the owner's goroutine.
func TestIgnoredLateRegistrationOriginalSnapshot(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	tracker, _ := NewIgnoredRejections(owner)
	cause := errors.New("original late observer reason")
	var observed []IgnoredRejection
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		holder, _ := NewOwnerFuture[int](owner)
		child, _ := NewOwnerFuture[int](owner)
		if err := child.Reject(scope, cause); err != nil {
			return err
		}
		if err := Ignore(scope, child, tracker, holder.Identity(), "original-late"); err != nil {
			return err
		}
		var err error
		observed, err = tracker.Pending(scope)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 || observed[0].Original != cause {
		t.Errorf("late actual dependent rejection missing: %v", observed)
	}
}
