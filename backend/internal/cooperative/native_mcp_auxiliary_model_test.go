package cooperative

import (
	"context"
	"errors"
	"testing"
)

func TestNativeMCPAuxiliaryModelsCanonicalMintAndSameOwnerInactiveLifecycle(t *testing.T) {
	owner, foreign := New(), New()
	defer owner.Close()
	defer foreign.Close()
	var proof *NativeMCPAuxiliaryModels
	var stale *Scope
	if e := owner.Run(context.Background(), func(scope *Scope) error {
		stale = scope
		var e error
		proof, e = NewNativeMCPAuxiliaryModels(scope, owner)
		if e != nil {
			return e
		}
		return scope.Inline(Read, func(callback *Scope) error {
			if _, e := NewNativeMCPAuxiliaryModels(callback, owner); !errors.Is(e, ErrRestricted) {
				t.Fatal("read minted MCP purpose", e)
			}
			return nil
		})
	}); e != nil {
		t.Fatal(e)
	}
	for _, source := range []string{"web_fetch_apply", "web_search_tool"} {
		receipt, e := owner.SubmitIndependent(context.Background(), Read, func(scope *Scope) error {
			if e := scope.Frame(FrameFeed, func() error { return nil }); !errors.Is(e, ErrRestricted) {
				t.Fatal("generic callback Frame guard changed", e)
			}
			return proof.Run(scope, context.Background(), source, func(next *Scope) error { return next.Frame(FrameFeed, func() error { return nil }) })
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			t.Fatal("actual inactive MCP producer rejected", e)
		}
	}
	if e := proof.Run(stale, context.Background(), "web_fetch_apply", func(*Scope) error { return nil }); !errors.Is(e, ErrRestricted) {
		t.Fatal("stale scope admitted", e)
	}
	receipt, e := foreign.SubmitIndependent(context.Background(), Read, func(scope *Scope) error {
		if e := proof.Run(scope, context.Background(), "web_fetch_apply", func(*Scope) error { return nil }); !errors.Is(e, ErrRestricted) {
			t.Fatal("foreign scope admitted", e)
		}
		return nil
	})
	if e == nil {
		e = receipt.Wait()
	}
	if e != nil {
		t.Fatal(e)
	}
	receipt, e = owner.SubmitIndependent(context.Background(), Read, func(scope *Scope) error {
		for _, source := range []string{"sdk", "repl_main_thread", "workflow_agent", ""} {
			if e := proof.Run(scope, context.Background(), source, func(*Scope) error { return nil }); !errors.Is(e, ErrRestricted) {
				t.Fatal("MCP widened model purpose", source, e)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if e := proof.Run(scope, ctx, "web_fetch_apply", func(*Scope) error { t.Fatal("cancelled model entered"); return nil }); !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
		return nil
	})
	if e == nil {
		e = receipt.Wait()
	}
	if e != nil {
		t.Fatal(e)
	}
}
