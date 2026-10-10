package cooperative

import (
	"context"
	"errors"
	"testing"
)

func TestInsightsAuthorityRequiresCanonicalActiveHostAndPreservesGenericGuards(t *testing.T) {
	owner := New()
	defer owner.Close()
	foreign := New()
	defer foreign.Close()
	var authority *NativeInsightsModels
	var stale *Scope
	if e := owner.Run(context.Background(), func(scope *Scope) error {
		stale = scope
		var e error
		authority, e = NewNativeInsightsModels(scope, owner)
		if e != nil {
			return e
		}
		if _, e = NewNativeInsightsModels(scope, foreign); !errors.Is(e, ErrRestricted) {
			t.Error("foreign authority construction", e)
		}
		if e = scope.Frame(FrameFeed, func() error {
			if _, e := NewNativeInsightsModels(scope, owner); !errors.Is(e, ErrRestricted) {
				t.Error("frame construction", e)
			}
			if e := authority.Run(scope, context.Background(), func(*Scope) error { return nil }); !errors.Is(e, ErrRestricted) {
				t.Error("nested frame admitted", e)
			}
			return nil
		}); e != nil {
			return e
		}
		return scope.Inline(Read, func(callback *Scope) error {
			if _, e := NewNativeInsightsModels(callback, owner); !errors.Is(e, ErrRestricted) {
				t.Error("callback minted authority", e)
			}
			if e := callback.Frame(FrameFeed, func() error { return nil }); !errors.Is(e, ErrRestricted) {
				t.Error("generic callback frame guard changed", e)
			}
			return authority.Run(callback, context.Background(), func(producer *Scope) error {
				if producer == callback || !producer.BelongsTo(owner) {
					t.Error("not fresh same owner")
				}
				return producer.Frame(FrameFeed, func() error { return nil })
			})
		})
	}); e != nil {
		t.Fatal(e)
	}
	if e := authority.Run(stale, context.Background(), func(*Scope) error { return nil }); !errors.Is(e, ErrRestricted) {
		t.Fatal("stale authority use", e)
	}
	if e := foreign.Run(context.Background(), func(scope *Scope) error {
		if e := authority.Run(scope, context.Background(), func(*Scope) error { return nil }); !errors.Is(e, ErrRestricted) {
			t.Error("foreign producer use", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := owner.Run(context.Background(), func(scope *Scope) error {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if e := authority.Run(scope, ctx, func(*Scope) error { t.Error("cancelled producer entered"); return nil }); !errors.Is(e, context.Canceled) {
			t.Error("lost model signal", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	receipt, e := owner.Submit(context.Background(), Read, func(scope *Scope) error {
		if e := authority.Run(scope, context.Background(), func(*Scope) error { return nil }); !errors.Is(e, ErrRestricted) {
			t.Error("no active ASK admitted", e)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = receipt.Wait(); e != nil {
		t.Fatal(e)
	}
}
