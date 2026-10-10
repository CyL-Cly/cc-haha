package ownerasync

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"testing"
)

func TestDelegateForwardsOriginalThrowDemand(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	original := errors.New("original supplied throw")
	seen := false
	e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var inner *OwnerIterator[string]
		inner, _ = NewOwnerIterator(owner, func(s *cooperative.Scope, it *OwnerIterator[string], r IteratorRequest[string]) error {
			return it.Yield(s, "before", func(s *cooperative.Scope, it *OwnerIterator[string], r IteratorRequest[string]) error {
				if !r.Throwing || r.Failure != original {
					return errors.New("Throw was changed into Next")
				}
				seen = true
				return it.Yield(s, "catch", func(s *cooperative.Scope, it *OwnerIterator[string], _ IteratorRequest[string]) error {
					return it.Complete(s)
				})
			})
		})
		var outer *OwnerIterator[string]
		outer, _ = NewOwnerIterator(owner, func(s *cooperative.Scope, it *OwnerIterator[string], r IteratorRequest[string]) error {
			return it.Delegate(s, inner, r, func(s *cooperative.Scope, _ IteratorResult[string], _ IteratorRequest[string]) error {
				return it.Complete(s)
			}, func(s *cooperative.Scope, e error) error { return it.Fail(s, e) })
		})
		f, e := outer.Next(scope)
		if e != nil {
			return e
		}
		r, e := f.Await(scope)
		if e != nil || r.Value != "before" {
			return errors.New("initial delegated yield")
		}
		f, e = outer.Throw(scope, original)
		if e != nil {
			return e
		}
		r, e = f.Await(scope)
		if e != nil {
			return e
		}
		if !seen || r.Done || r.Value != "catch" {
			return errors.New("original Throw catch yield not forwarded")
		}
		f, e = outer.Next(scope)
		if e != nil {
			return e
		}
		r, e = f.Await(scope)
		if e != nil || !r.Done {
			return errors.New("catch continuation did not complete")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
