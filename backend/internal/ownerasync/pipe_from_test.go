package ownerasync

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestOwnerFuturePipeFromForwardsOneInternalReactionAndKeepsIdentity(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "fulfilled", true: "rejected"}[rejected], func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			value := &struct{ Name string }{"module namespace"}
			failure := errors.New("module evaluation failure")
			if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				source, _ := NewOwnerFuture[*struct{ Name string }](owner)
				target, _ := NewOwnerFuture[*struct{ Name string }](owner)
				if rejected {
					_ = source.Reject(scope, failure)
				} else {
					_ = source.Resolve(scope, value)
				}
				tick, completed, completion := 0, false, -1
				_, err := target.Then(scope, func(_ *cooperative.Scope, got *struct{ Name string }, gotError error) error {
					completion, completed = tick, true
					if rejected && gotError != failure || !rejected && (gotError != nil || got != value) {
						t.Error("forward changed original identity", got, gotError)
					}
					return nil
				})
				if err != nil {
					return err
				}
				if err = target.PipeFrom(scope, source); err != nil {
					return err
				}
				// A claimed internal target cannot be overwritten before forwarding.
				if err = target.Resolve(scope, nil); err != nil {
					return err
				}
				var probe func(*cooperative.Scope) error
				probe = func(*cooperative.Scope) error {
					tick++
					if !completed {
						_, e := owner.Microtask(cooperative.Read, probe)
						return e
					}
					return nil
				}
				if _, err = owner.Microtask(cooperative.Read, probe); err != nil {
					return err
				}
				got, gotError := target.Await(scope)
				if completion != 1 || rejected && gotError != failure || !rejected && (gotError != nil || got != value) {
					t.Error("internal forwarding/adoption boundary", completion, got, gotError)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOwnerFuturePipeFromRejectsForeignExpiredSelfAndClosedRegistration(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	target, _ := NewOwnerFuture[string](owner)
	source, _ := NewOwnerFuture[string](owner)
	other, _ := NewOwnerFuture[string](foreign)
	var expired *cooperative.Scope
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		expired = scope
		for _, candidate := range []*OwnerFuture[string]{nil, target, other} {
			if err := target.PipeFrom(scope, candidate); !errors.Is(err, cooperative.ErrRestricted) {
				t.Error("invalid source admitted", err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := target.PipeFrom(expired, source); !errors.Is(err, cooperative.ErrRestricted) {
		t.Error("expired scope admitted", err)
	}
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := target.PipeFrom(scope, source); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign owner admitted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	owner.Close()
	if _, err := target.Wait(context.Background()); !errors.Is(err, cooperative.ErrClosed) {
		t.Error("close did not reject target", err)
	}
}

func TestOwnerFuturePipeFromPendingSourceCancelDoesNotCancelIndependentForwarding(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	source, _ := NewOwnerFuture[string](owner)
	target, _ := NewOwnerFuture[string](owner)
	ctx, cancel := context.WithCancel(context.Background())
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		if err := target.PipeFrom(scope, source); err != nil {
			return err
		}
		cancel()
		_, err := target.Await(scope)
		if !errors.Is(err, context.Canceled) {
			t.Error("cancelled caller remained waiting", err)
		}
		return nil
	}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := source.Resolve(scope, "admitted"); err != nil {
			return err
		}
		value, err := target.Await(scope)
		if err != nil || value != "admitted" {
			t.Error("caller cancel discarded independent pipe", value, err)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
