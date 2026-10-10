package runtimecleanup_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
)

func run(t *testing.T, body func(*cooperative.Owner, *cooperative.Scope, *runtimecleanup.Registry) error) {
	t.Helper()
	owner := cooperative.New()
	defer owner.Close()
	registry, err := runtimecleanup.New(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		return body(owner, scope, registry)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFunctionIdentityUnregisterAndReinsertionOrder(t *testing.T) {
	run(t, func(owner *cooperative.Owner, scope *cooperative.Scope, registry *runtimecleanup.Registry) error {
		var trace []string
		callback := func(id string) *runtimecleanup.Callback {
			return runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) {
				trace = append(trace, id)
				return nil, nil
			})
		}
		a, b := callback("A"), callback("B")
		removeA, err := registry.Register(scope, a)
		if err != nil {
			return err
		}
		duplicate, _ := registry.Register(scope, a)
		_, _ = registry.Register(scope, b)
		removed, err := duplicate(scope)
		if err != nil || !removed {
			return fmt.Errorf("duplicate unregister: %v %v", removed, err)
		}
		removed, err = removeA(scope)
		if err != nil || removed {
			return fmt.Errorf("original unregister must see removed identity: %v %v", removed, err)
		}
		_, _ = registry.Register(scope, a)
		future, err := registry.Start(scope)
		if err != nil {
			return err
		}
		if _, err := future.Await(scope); err != nil {
			return err
		}
		if !reflect.DeepEqual(trace, []string{"B", "A"}) {
			return fmt.Errorf("reinserted Set order: %v", trace)
		}
		return nil
	})
}

func TestEveryCallbackStartsBeforeHeldPromisesSettle(t *testing.T) {
	run(t, func(owner *cooperative.Owner, scope *cooperative.Scope, registry *runtimecleanup.Registry) error {
		a, _ := ownerasync.NewOwnerFuture[struct{}](owner)
		b, _ := ownerasync.NewOwnerFuture[struct{}](owner)
		var trace []string
		for index, original := range []*runtimecleanup.Future{a, b} {
			_, err := registry.Register(scope, runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) {
				trace = append(trace, fmt.Sprint(index))
				return original, nil
			}))
			if err != nil {
				return err
			}
		}
		future, err := registry.Start(scope)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(trace, []string{"0", "1"}) {
			return fmt.Errorf("cleanup must not serialize callback startup: %v", trace)
		}
		_ = b.Resolve(scope, struct{}{})
		select {
		case <-future.Done():
			return errors.New("cleanup fulfilled before all real callbacks")
		default:
		}
		_ = a.Resolve(scope, struct{}{})
		_, err = future.Await(scope)
		return err
	})
}

func TestAsyncRejectionKeepsLaterCallbackAliveAndOriginalReason(t *testing.T) {
	run(t, func(owner *cooperative.Owner, scope *cooperative.Scope, registry *runtimecleanup.Registry) error {
		original := errors.New("original native rejection")
		rejected, _ := ownerasync.NewOwnerFuture[struct{}](owner)
		peer, _ := ownerasync.NewOwnerFuture[struct{}](owner)
		_ = rejected.Reject(scope, original)
		starts := 0
		for _, returned := range []*runtimecleanup.Future{rejected, peer} {
			_, _ = registry.Register(scope, runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) {
				starts++
				return returned, nil
			}))
		}
		future, err := registry.Start(scope)
		if err != nil {
			return err
		}
		if _, err = future.Await(scope); err != original || starts != 2 {
			return fmt.Errorf("async rejection reason/start count: %v/%d", err, starts)
		}
		select {
		case <-peer.Done():
			return errors.New("rejected Promise.all cancelled unrelated cleanup")
		default:
		}
		_ = peer.Resolve(scope, struct{}{})
		_, err = peer.Await(scope)
		return err
	})
}

func TestSynchronousThrowStopsMapWithoutJoiningEarlierWork(t *testing.T) {
	run(t, func(owner *cooperative.Owner, scope *cooperative.Scope, registry *runtimecleanup.Registry) error {
		pending, _ := ownerasync.NewOwnerFuture[struct{}](owner)
		original := errors.New("synchronous throw")
		var trace []string
		_, _ = registry.Register(scope, runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) {
			trace = append(trace, "first")
			return pending, nil
		}))
		_, _ = registry.Register(scope, runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) {
			trace = append(trace, "throw")
			return nil, original
		}))
		_, _ = registry.Register(scope, runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) {
			trace = append(trace, "forbidden third")
			return nil, nil
		}))
		future, err := registry.Start(scope)
		if err != nil {
			return err
		}
		if _, err = future.Await(scope); err != original || !reflect.DeepEqual(trace, []string{"first", "throw"}) {
			return fmt.Errorf("map throw: %v %v", err, trace)
		}
		select {
		case <-pending.Done():
			return errors.New("synchronous map throw cancelled earlier callback")
		default:
		}
		return pending.Resolve(scope, struct{}{})
	})
}

func TestRegistryMutationsAffectNextSnapshot(t *testing.T) {
	run(t, func(owner *cooperative.Owner, scope *cooperative.Scope, registry *runtimecleanup.Registry) error {
		var trace []string
		b := runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) { trace = append(trace, "B"); return nil, nil })
		c := runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) { trace = append(trace, "C"); return nil, nil })
		var removeB runtimecleanup.Unregister
		a := runtimecleanup.NewCallback(func(next *cooperative.Scope) (*runtimecleanup.Future, error) {
			trace = append(trace, "A")
			_, err := removeB(next)
			if err != nil {
				return nil, err
			}
			_, err = registry.Register(next, c)
			return nil, err
		})
		_, _ = registry.Register(scope, a)
		removeB, _ = registry.Register(scope, b)
		for range 2 {
			future, err := registry.Start(scope)
			if err != nil {
				return err
			}
			if _, err = future.Await(scope); err != nil {
				return err
			}
		}
		if !reflect.DeepEqual(trace, []string{"A", "B", "A", "C"}) {
			return fmt.Errorf("snapshot changed during map: %v", trace)
		}
		return nil
	})
}

func TestEmptyRegistryStillReturnsRealAsyncCompletion(t *testing.T) {
	run(t, func(owner *cooperative.Owner, scope *cooperative.Scope, registry *runtimecleanup.Registry) error {
		future, err := registry.Start(scope)
		if err != nil {
			return err
		}
		select {
		case <-future.Done():
			return errors.New("async registry returned a synchronously fulfilled result")
		default:
		}
		_, err = future.Await(scope)
		return err
	})
}

func TestWrongOwnerAndInvalidCallbacksHaveNoEffects(t *testing.T) {
	if registry, err := runtimecleanup.New(nil); registry != nil || err != cooperative.ErrRestricted {
		t.Fatalf("nil owner: %v %v", registry, err)
	}
	run(t, func(owner *cooperative.Owner, scope *cooperative.Scope, registry *runtimecleanup.Registry) error {
		for _, callback := range []*runtimecleanup.Callback{nil, {}} {
			if _, err := registry.Register(scope, callback); err != cooperative.ErrRestricted {
				return fmt.Errorf("invalid callback: %v", err)
			}
		}
		foreign := cooperative.New()
		defer foreign.Close()
		other, _ := runtimecleanup.New(foreign)
		if _, err := other.Register(scope, runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) { return nil, nil })); err != cooperative.ErrRestricted {
			return fmt.Errorf("foreign register: %v", err)
		}
		if _, err := other.Start(scope); err != cooperative.ErrRestricted {
			return fmt.Errorf("foreign Start: %v", err)
		}
		remove, _ := registry.Register(scope, runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) { return nil, nil }))
		if removed, err := remove(nil); removed || err != cooperative.ErrRestricted {
			return fmt.Errorf("invalid unregister: %v %v", removed, err)
		}
		return nil
	})
}

func TestForeignPromiseIsRejectedWithoutRunningItsReaction(t *testing.T) {
	foreign := cooperative.New()
	defer foreign.Close()
	value, _ := ownerasync.NewOwnerFuture[struct{}](foreign)
	run(t, func(owner *cooperative.Owner, scope *cooperative.Scope, registry *runtimecleanup.Registry) error {
		_, _ = registry.Register(scope, runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) { return value, nil }))
		future, err := registry.Start(scope)
		if err != nil {
			return err
		}
		_, err = future.Await(scope)
		if err != cooperative.ErrRestricted {
			return fmt.Errorf("cross-owner future: %v", err)
		}
		return nil
	})
}

func TestCallbackCancellingCallerCannotStrandCleanupResult(t *testing.T) {
	for _, throw := range []bool{false, true} {
		t.Run(fmt.Sprint(throw), func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			registry, _ := runtimecleanup.New(owner)
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			cause := errors.New("original caller cancellation")
			var result *runtimecleanup.Future
			err := owner.Run(ctx, func(scope *cooperative.Scope) error {
				_, err := registry.Register(scope, runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) {
					cancel(cause)
					if throw {
						return nil, errors.New("callback throw")
					}
					return nil, nil
				}))
				if err != nil {
					return err
				}
				result, err = registry.Start(scope)
				if err != cause {
					return fmt.Errorf("Start administrative error: %v", err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if result == nil {
				t.Fatal("missing admitted cleanup result")
			}
			select {
			case <-result.Done():
			default:
				t.Fatal("cleanup result is stranded until Owner.Close")
			}
			if _, err := result.Wait(context.Background()); err != cause {
				t.Fatalf("result lost original administrative cause: %v", err)
			}
		})
	}
}
