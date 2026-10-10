package cooperative

import (
	"context"
	"errors"
	"testing"
)

func TestNativeAutoModeModelsUseOnlyOriginalCanonicalOwner(t *testing.T) {
	owner := New()
	other := New()
	defer owner.Close()
	defer other.Close()
	calls := 0
	if e := owner.Run(context.Background(), func(scope *Scope) error {
		if _, e := NewNativeAutoModeModels(scope, other); !errors.Is(e, ErrRestricted) {
			t.Fatal("foreign producer minted", e)
		}
		a, e := NewNativeAutoModeModels(scope, owner)
		if e != nil {
			return e
		}
		if !a.Owns(owner) || a.Owns(other) || a.Owns(nil) {
			t.Fatal("authority lost canonical identity")
		}
		e = a.Run(scope, context.Background(), func(actual *Scope) error {
			calls++
			if !actual.BelongsTo(owner) {
				t.Fatal("second owner used")
			}
			return nil
		})
		if e != nil {
			return e
		}
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if e := a.Run(scope, cancelled, func(*Scope) error { calls++; return nil }); !errors.Is(e, context.Canceled) {
			t.Fatal("cancelled producer admitted", e)
		}
		var absent *NativeAutoModeModels
		if e := absent.Run(scope, context.Background(), func(*Scope) error { calls++; return nil }); !errors.Is(e, ErrRestricted) {
			t.Fatal("nil authority admitted", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal("unauthorized side query consumer ran", calls)
	}
}
