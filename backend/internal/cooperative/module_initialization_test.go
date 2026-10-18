package cooperative

import (
	"context"
	"errors"
	"testing"
)

func TestModuleInitializationAdmissionIsOwnerLifetimeRestrictedAndNotInline(t *testing.T) {
	owner, foreign := New(), New()
	defer owner.Close()
	defer foreign.Close()
	called := 0
	if _, err := ((*Scope)(nil)).SubmitModuleInitialization(owner, func(*Scope) error { return nil }); err != ErrRestricted {
		t.Fatal("nil scope admitted", err)
	}
	if err := foreign.Run(context.Background(), func(scope *Scope) error {
		_, err := scope.SubmitModuleInitialization(owner, func(*Scope) error { called++; return nil })
		if err != ErrRestricted {
			t.Error("foreign scope admitted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var retained *Scope
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		retained = scope
		if _, err := scope.SubmitModuleInitialization(owner, nil); err != ErrRestricted {
			t.Error("nil initializer admitted", err)
		}
		receipt, err := scope.SubmitModuleInitialization(owner, func(next *Scope) error {
			called++
			if next.ctx != owner.life {
				t.Error("module job inherited caller context")
			}
			if err := next.Frame(FrameFeed, func() error { t.Error("module job acquired Frame"); return nil }); err != ErrRestricted {
				t.Error("module job not callback restricted", err)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if called != 0 {
			t.Error("actual module work ran inline")
		}
		return scope.AwaitPromise(receipt.Done())
	}); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatal("actual owner module work count", called)
	}
	if _, err := retained.SubmitModuleInitialization(owner, func(*Scope) error { return nil }); err != ErrRestricted {
		t.Fatal("retained scope admitted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := owner.Run(ctx, func(scope *Scope) error {
		cancel()
		_, err := scope.SubmitModuleInitialization(owner, func(*Scope) error { called++; return nil })
		if !errors.Is(err, context.Canceled) {
			t.Error("cancelled admission accepted", err)
		}
		return nil
	}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatal("invalid admission executed real work", called)
	}
}
