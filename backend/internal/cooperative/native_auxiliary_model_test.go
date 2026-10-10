package cooperative

import (
	"context"
	"errors"
	"testing"
)

func TestAuxiliaryAuthorityRequiresCanonicalActiveHostAndPreservesGenericGuards(t *testing.T) {
	owner := New()
	defer owner.Close()
	foreign := New()
	defer foreign.Close()
	var authority *NativeAuxiliaryModels
	var stale *Scope
	if e := owner.Run(context.Background(), func(scope *Scope) error {
		stale = scope
		var e error
		authority, e = NewNativeAuxiliaryModels(scope, owner)
		if e != nil {
			return e
		}
		if _, e = NewNativeAuxiliaryModels(scope, foreign); !errors.Is(e, ErrRestricted) {
			t.Error("foreign authority construction", e)
		}
		if e = scope.Frame(FrameFeed, func() error {
			if _, e := NewNativeAuxiliaryModels(scope, owner); !errors.Is(e, ErrRestricted) {
				t.Error("frame construction", e)
			}
			if e := authority.Run(scope, context.Background(), "web_fetch_apply", func(*Scope) error { return nil }); !errors.Is(e, ErrRestricted) {
				t.Error("nested frame admitted", e)
			}
			return nil
		}); e != nil {
			return e
		}
		return scope.Inline(Read, func(callback *Scope) error {
			if _, e := NewNativeAuxiliaryModels(callback, owner); !errors.Is(e, ErrRestricted) {
				t.Error("callback minted authority", e)
			}
			if e := callback.Frame(FrameFeed, func() error { return nil }); !errors.Is(e, ErrRestricted) {
				t.Error("generic callback frame guard changed", e)
			}
			return authority.Run(callback, context.Background(), "web_fetch_apply", func(producer *Scope) error {
				if producer == callback || !producer.BelongsTo(owner) {
					t.Error("not fresh same owner")
				}
				return producer.Frame(FrameFeed, func() error { return nil })
			})
		})
	}); e != nil {
		t.Fatal(e)
	}
	if e := authority.Run(stale, context.Background(), "web_fetch_apply", func(*Scope) error { return nil }); !errors.Is(e, ErrRestricted) {
		t.Fatal("stale authority use", e)
	}
	if e := foreign.Run(context.Background(), func(scope *Scope) error {
		if e := authority.Run(scope, context.Background(), "web_fetch_apply", func(*Scope) error { return nil }); !errors.Is(e, ErrRestricted) {
			t.Error("foreign producer use", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := owner.Run(context.Background(), func(scope *Scope) error {
		for _, source := range []string{"sdk", "agent:custom", "unproven", ""} {
			if e := authority.Run(scope, context.Background(), source, func(*Scope) error { return nil }); !errors.Is(e, ErrRestricted) {
				t.Error("unsupported source", source, e)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if e := authority.Run(scope, ctx, "web_search_tool", func(*Scope) error { t.Error("cancelled producer entered"); return nil }); !errors.Is(e, context.Canceled) {
			t.Error("lost model signal", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	receipt, e := owner.Submit(context.Background(), Read, func(scope *Scope) error {
		if e := authority.Run(scope, context.Background(), "web_fetch_apply", func(*Scope) error { return nil }); !errors.Is(e, ErrRestricted) {
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
