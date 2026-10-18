package nativemodule

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

type testNamespace struct{ name string }

func linkedTestEntry(t *testing.T, owner *cooperative.Owner, scope *cooperative.Scope) *Entry[string, *testNamespace] {
	t.Helper()
	namespace := &testNamespace{name: "actual-linked-test-module"}
	entry, err := NewPrelinked(owner, scope, "module", namespace, func(next *cooperative.Scope, key string, got *testNamespace) error {
		if !next.BelongsTo(owner) || key != "module" || got != namespace {
			return cooperative.ErrRestricted
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestPrelinkedImportsPreserveNamespaceAndDistinctConsumerPromises(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		entry := linkedTestEntry(t, owner, scope)
		one, err := entry.Import(scope)
		if err != nil {
			return err
		}
		two, err := entry.Import(scope)
		if err != nil {
			return err
		}
		if one == two {
			t.Error("two imports reused the public Promise")
		}
		first, err := one.Await(scope)
		if err != nil {
			return err
		}
		second, err := two.Await(scope)
		if err != nil {
			return err
		}
		if first != second || first != entry.namespace {
			t.Error("imports replaced the original module namespace")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPrelinkedJoinRequiresBothArmsAndPreservesFirstRejection(t *testing.T) {
	for _, stateFirst := range []bool{false, true} {
		for _, reject := range []bool{false, true} {
			owner := cooperative.New()
			failure := errors.New("original evaluation failure")
			if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				entry := linkedTestEntry(t, owner, scope)
				result, _ := ownerasync.NewOwnerFuture[*testNamespace](owner)
				join := &moduleJoin[string, *testNamespace]{entry: entry, remaining: 2, result: result}
				var namespace *testNamespace
				if stateFirst {
					namespace = entry.namespace
				}
				var firstFailure error
				if reject {
					firstFailure = failure
				}
				if err := join.settle(scope, namespace, firstFailure); err != nil {
					return err
				}
				select {
				case <-result.Done():
					if !reject {
						t.Error("one successful load arm released import")
					}
				default:
					if reject {
						t.Error("failed load arm left import pending")
					}
				}
				namespace = nil
				if !stateFirst {
					namespace = entry.namespace
				}
				if err := join.settle(scope, namespace, nil); err != nil {
					return err
				}
				got, err := result.Await(scope)
				if reject {
					if err != failure {
						t.Error("late success replaced the first rejection", err)
					}
				} else if err != nil || got != entry.namespace {
					t.Error("two-arm join changed the namespace", got, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			owner.Close()
		}
	}
}

func TestPrelinkedRejectsForeignExpiredNilAndCanceledConstruction(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	var entry *Entry[string, *testNamespace]
	var expired *cooperative.Scope
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		expired, entry = scope, linkedTestEntry(t, owner, scope)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Import(expired); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("expired import scope admitted", err)
	}
	if _, err := (*Entry[string, *testNamespace])(nil).Import(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("nil import admitted", err)
	}
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := entry.Import(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign import admitted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	validated := false
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		cancel()
		_, err := NewPrelinked(owner, scope, "module", &testNamespace{}, func(*cooperative.Scope, string, *testNamespace) error { validated = true; return nil })
		if !errors.Is(err, context.Canceled) || validated {
			t.Error("canceled construction invoked namespace validator", err, validated)
		}
		return nil
	}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPrelinkedPendingImportCancelsWaitAndClosesWithoutLateSuccess(t *testing.T) {
	owner := cooperative.New()
	ctx, cancel := context.WithCancel(context.Background())
	var pending *ownerasync.OwnerFuture[*testNamespace]
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		entry := linkedTestEntry(t, owner, scope)
		entry.evaluation, _ = ownerasync.NewOwnerFuture[struct{}](owner)
		var err error
		pending, err = entry.Import(scope)
		if err != nil {
			return err
		}
		cancel()
		if _, err = pending.Await(scope); !errors.Is(err, context.Canceled) {
			t.Error("canceled pending import did not settle its waiter", err)
		}
		return nil
	}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	owner.Close()
	if _, err := pending.Wait(context.Background()); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("closed Owner left a pending namespace import", err)
	}
}

func TestPrelinkedCachedFailureRemainsIdenticalAcrossImports(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	failure := errors.New("original cached module failure")
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		entry := linkedTestEntry(t, owner, scope)
		entry.failure = failure
		for range 2 {
			pending, err := entry.Import(scope)
			if err != nil {
				return err
			}
			if _, err = pending.Await(scope); err != failure {
				t.Error("cached rejection identity changed", err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
