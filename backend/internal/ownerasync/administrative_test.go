package ownerasync

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestAdministrativeRejectionAndAbandonRetirePendingYield(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	original := errors.New("independent ASK administrative cancellation")
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		value, _ := NewOwnerFuture[string](owner)
		continued := false
		iterator, _ := NewOwnerIterator(owner, func(scope *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
			return iterator.YieldFuture(scope, value, func(scope *cooperative.Scope, iterator *OwnerIterator[string], _ IteratorRequest[string]) error {
				continued = true
				return iterator.Complete(scope)
			}, nil)
		})
		first, _ := iterator.Next(scope)
		queued, _ := iterator.Next(scope)
		if err := iterator.Abandon(scope, original); err != nil {
			return err
		}
		if err := value.Resolve(scope, "late original producer result"); err != nil {
			return err
		}
		for _, future := range []*OwnerFuture[IteratorResult[string]]{first, queued} {
			if _, failure := future.Await(scope); failure != original {
				t.Fatal("administrative rejection identity lost", failure)
			}
		}
		done, _ := iterator.Next(scope)
		result, err := done.Await(scope)
		if err != nil || !result.Done || continued || iterator.active != nil || len(iterator.queue) > 0 || iterator.resume != nil {
			t.Fatal("late pending yield resurrected original continuation", result, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestAdministrativeAbandonRetiresPendingAwaitAdoptionAndReturn(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	failure := errors.New("original owner obligation abandoned")
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		source, _ := NewOwnerFuture[int](owner)
		adopted, _ := NewOwnerFuture[int](owner)
		_ = adopted.ResolveFrom(scope, source)
		if err := adopted.RejectAdministrative(failure); err != nil {
			return err
		}
		if _, err := adopted.Await(scope); err != failure {
			t.Fatal(err)
		}
		_ = source.Resolve(scope, 9)
		resumed := false
		iterator, _ := NewOwnerIterator(owner, func(scope *cooperative.Scope, iterator *OwnerIterator[int], _ IteratorRequest[int]) error {
			return AwaitGenerator(scope, iterator, source, func(scope *cooperative.Scope, _ int) error { resumed = true; return iterator.CompleteValue(scope, 8) }, nil)
		})
		first, _ := iterator.Next(scope)
		if err := iterator.Abandon(scope, failure); err != nil {
			return err
		}
		if _, err := first.Await(scope); err != failure || resumed {
			t.Fatal("late awaited continuation ran", err, resumed)
		}
		closing, _ := NewOwnerIterator(owner, func(scope *cooperative.Scope, iterator *OwnerIterator[int], _ IteratorRequest[int]) error {
			return iterator.Yield(scope, 1, func(scope *cooperative.Scope, iterator *OwnerIterator[int], _ IteratorRequest[int]) error {
				return iterator.CompleteReturn(scope)
			})
		})
		first, _ = closing.Next(scope)
		_, _ = first.Await(scope)
		returned, _ := closing.Return(scope, 4)
		if err := closing.Abandon(scope, failure); err != nil {
			return err
		}
		if _, err := returned.Await(scope); err != failure {
			t.Fatal(err)
		}
		valueReturn, _ := NewOwnerIterator(owner, func(scope *cooperative.Scope, iterator *OwnerIterator[int], _ IteratorRequest[int]) error {
			return iterator.CompleteValue(scope, 7)
		})
		completed, _ := valueReturn.Next(scope)
		_ = valueReturn.Abandon(scope, failure)
		if _, err := completed.Await(scope); err != failure {
			t.Fatal(err)
		}
		if err := iterator.Abandon(scope, nil); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal(err)
		}
		var absent *OwnerFuture[int]
		if err := absent.RejectAdministrative(failure); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal(err)
		}
		if err := source.RejectAdministrative(nil); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal(err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestAdministrativeAbandonForeignOwnerRejectedBeforeMutation(t *testing.T) {
	first, second := cooperative.New(), cooperative.New()
	defer first.Close()
	defer second.Close()
	iterator, _ := NewOwnerIterator(first, func(scope *cooperative.Scope, iterator *OwnerIterator[int], _ IteratorRequest[int]) error {
		return iterator.Complete(scope)
	})
	if err := second.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := iterator.Abandon(scope, errors.New("foreign")); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal(err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if iterator.completed || iterator.generation != 0 {
		t.Fatal("foreign administrative callback changed original iterator")
	}
}
