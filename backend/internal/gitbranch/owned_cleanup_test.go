package gitbranch

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestOwnedSDKGitCleanupRegistersOnActualSettlementOwner(t *testing.T) {
	calls := 0
	fault := errors.New("registry rejected")
	h := newHarness(t, func(o *Options) {
		o.RegisterCleanup = nil
		o.RegisterCleanupOwned = func(scope *cooperative.Scope, cleanup func(*cooperative.Scope) error) error {
			if !scope.BelongsTo(o.Owner) || cleanup == nil {
				return cooperative.ErrRestricted
			}
			calls++
			return fault
		}
	})
	h.write(t, ".git/HEAD", "ref: refs/heads/original\n")
	var f *BranchFuture
	h.call(t, func(scope *cooperative.Scope) error { var e error; f, e = h.s.Branch(scope); return e })
	if _, e := f.Wait(context.Background()); !errors.Is(e, fault) || calls != 1 {
		t.Fatalf("registration failure/order %v %d", e, calls)
	}
}

func TestOwnedSDKGitCleanupCapabilitiesCannotOverlap(t *testing.T) {
	h := newHarness(t, nil)
	options := h.s.options
	options.RegisterCleanupOwned = func(*cooperative.Scope, func(*cooperative.Scope) error) error { return nil }
	if _, e := New(options); e == nil {
		t.Fatal("two registry authorities admitted")
	}
	options.RegisterCleanup = nil
	options.RegisterCleanupOwned = nil
	if _, e := New(options); e == nil {
		t.Fatal("cleanup silently absent")
	}
}
