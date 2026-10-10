package cooperative

import (
	"context"
	"errors"
	"testing"
)

func TestAgentHookAuthorityRequiresActualHostAndDoesNotWidenCallbacks(t *testing.T) {
	owner, foreign := New(), New()
	defer owner.Close()
	defer foreign.Close()
	var proof *NativeAgentHookModels
	var stale *Scope
	if e := owner.Run(context.Background(), func(scope *Scope) error {
		stale = scope
		var e error
		proof, e = NewNativeAgentHookModels(scope, owner)
		if e != nil {
			return e
		}
		if _, e = NewNativeAgentHookModels(scope, foreign); !errors.Is(e, ErrRestricted) {
			return errors.New("foreign owner minted proof")
		}
		return scope.Inline(Read, func(callback *Scope) error {
			if _, e := NewNativeAgentHookModels(callback, owner); !errors.Is(e, ErrRestricted) {
				return errors.New("callback minted proof")
			}
			if e := callback.Frame(FrameFeed, func() error { return nil }); !errors.Is(e, ErrRestricted) {
				return errors.New("generic frame widened")
			}
			return proof.Run(callback, context.Background(), func(producer *Scope) error {
				if producer == callback || !producer.BelongsTo(owner) {
					return errors.New("different model owner")
				}
				return producer.Frame(FrameFeed, func() error { return nil })
			})
		})
	}); e != nil {
		t.Fatal(e)
	}
	if e := proof.Run(stale, context.Background(), func(*Scope) error { return nil }); !errors.Is(e, ErrRestricted) {
		t.Fatal("stale scope", e)
	}
	if e := owner.Run(context.Background(), func(scope *Scope) error {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if e := proof.Run(scope, ctx, func(*Scope) error { return errors.New("cancelled producer entered") }); !errors.Is(e, context.Canceled) {
			return errors.New("agent hook producer lost actual cancellation")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
