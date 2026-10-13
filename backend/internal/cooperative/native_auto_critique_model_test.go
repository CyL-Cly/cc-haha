package cooperative

import (
	"context"
	"errors"
	"testing"
)

func TestAutoCritiqueAuthorityCannotCrossOwnerCallbackOrLifetime(t *testing.T) {
	owner, other := New(), New()
	defer owner.Close()
	defer other.Close()
	var authority *NativeAutoCritiqueModels
	var expired *Scope
	calls := 0
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		expired = scope
		if _, err := NewNativeAutoCritiqueModels(scope, other); !errors.Is(err, ErrRestricted) {
			t.Fatal("foreign command owner minted model authority", err)
		}
		var err error
		authority, err = NewNativeAutoCritiqueModels(scope, owner)
		if err != nil {
			return err
		}
		if err = authority.Run(scope, context.Background(), func(current *Scope) error {
			calls++
			if !current.BelongsTo(owner) {
				t.Fatal("critique request changed command owner")
			}
			return nil
		}); err != nil {
			return err
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err = authority.Run(scope, ctx, func(*Scope) error { calls++; return nil }); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled command entered model", err)
		}
		return scope.Inline(Read, func(callback *Scope) error {
			if _, err := NewNativeAutoCritiqueModels(callback, owner); !errors.Is(err, ErrRestricted) {
				t.Fatal("read callback minted model authority", err)
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := authority.Run(expired, context.Background(), func(*Scope) error { calls++; return nil }); err == nil {
		t.Fatal("expired command scope entered model")
	}
	if calls != 1 {
		t.Fatal("unauthorized model work ran", calls)
	}
}
