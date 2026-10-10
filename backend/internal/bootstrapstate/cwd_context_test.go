package bootstrapstate

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

func TestCWDContextDefaultLiveAndAsyncOverrideInheritance(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	state, _ := New(owner, "A", "/base", func() (string, error) { return "B", nil })
	var inherited, nested, sibling *CWDContext
	var resumed *ownerasync.OwnerFuture[string]
	var stale *cooperative.Scope
	run(t, owner, func(scope *cooperative.Scope) error {
		stale = scope
		ctx, e := state.Context(scope)
		if e != nil {
			return e
		}
		if e = ctx.RunWithOverride(scope, "/override-e\u0301", func(next *CWDContext) error {
			inherited = next
			return next.RunWithOverride(scope, "", func(inner *CWDContext) error { nested = inner; return nil })
		}); e != nil {
			return e
		}
		sibling = ctx
		if e = state.SetCWD(scope, "/late-e\u0301"); e != nil {
			return e
		}
		value, e := ctx.GetCWD(scope)
		if e != nil {
			return e
		}
		if value != "/late-é" {
			t.Error("default context captured state", value)
		}
		value, e = inherited.GetCWD(scope)
		if e != nil {
			return e
		}
		if value != "/override-e\u0301" {
			t.Error("override normalized/restored", value)
		}
		value, e = nested.GetCWD(scope)
		if e != nil {
			return e
		}
		if value != "" {
			t.Error("empty override fell back", value)
		}
		// A real fulfilled Promise reaction retains the lexical async token after
		// runWithOverride's synchronous frame returned; no State set/restore occurs.
		trigger, _ := ownerasync.NewOwnerFuture[struct{}](owner)
		resumed, _ = ownerasync.NewOwnerFuture[string](owner)
		_, e = trigger.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
			if failure != nil {
				return resumed.Reject(next, failure)
			}
			value, e := inherited.GetCWD(next)
			if e != nil {
				return resumed.Reject(next, e)
			}
			return resumed.Resolve(next, value)
		})
		if e != nil {
			return e
		}
		return trigger.Resolve(scope, struct{}{})
	})
	run(t, owner, func(scope *cooperative.Scope) error {
		value, e := resumed.Await(scope)
		if e != nil {
			return e
		}
		if value != "/override-e\u0301" {
			t.Error(value)
		}
		value, e = sibling.GetCWD(scope)
		if e != nil {
			return e
		}
		if value != "/late-é" {
			t.Error("sibling overridden", value)
		}
		if !inherited.Owns(state) {
			t.Error("context identity lost")
		}
		return nil
	})
	if _, e := inherited.GetCWD(stale); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error("stale scope accepted", e)
	}
}

func TestCWDContextCapabilitiesAndPlanSlugLookup(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	other := cooperative.New()
	defer other.Close()
	state, _ := New(owner, "A", "/base", func() (string, error) { return "B", nil })
	var ctx *CWDContext
	var cache *PlanSlugs
	run(t, owner, func(scope *cooperative.Scope) error {
		var e error
		ctx, e = state.Context(scope)
		if e != nil {
			return e
		}
		cache, e = state.PlanSlugCache(scope)
		if e != nil {
			return e
		}
		if _, ok, e := cache.Get(scope, "absent"); e != nil || ok {
			t.Error(ok, e)
		}
		if e = cache.Set(scope, "A", "initial"); e != nil {
			return e
		}
		if e = cache.Set(scope, "A", "live"); e != nil {
			return e
		}
		value, ok, e := cache.Get(scope, "A")
		if e != nil || !ok || value != "live" {
			t.Error(value, ok, e)
		}
		if _, _, e = cache.Get(scope, string([]byte{0xff})); !errors.Is(e, ErrStringDomain) {
			t.Error(e)
		}
		if e = ctx.RunWithOverride(scope, string([]byte{0xff}), func(*CWDContext) error { t.Error("invalid override body executed"); return nil }); !errors.Is(e, ErrStringDomain) {
			t.Error(e)
		}
		sentinel := errors.New("original body throw")
		if e = ctx.RunWithOverride(scope, "/throw-override", func(*CWDContext) error { return sentinel }); !errors.Is(e, sentinel) {
			t.Error(e)
		}
		if value, e := ctx.GetCWD(scope); e != nil || value != "/base" {
			t.Error("body throw changed shared CWD", value, e)
		}
		if e = ctx.RunWithOverride(scope, "/a", nil); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		return nil
	})
	run(t, other, func(scope *cooperative.Scope) error {
		if _, e := state.Context(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		if _, e := ctx.GetCWD(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		if e := ctx.RunWithOverride(scope, "/x", func(*CWDContext) error { t.Error("foreign body executed"); return nil }); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		if _, _, e := cache.Get(scope, "A"); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		return nil
	})
	var absent *CWDContext
	var absentState *State
	var absentCache *PlanSlugs
	if _, e := absent.GetCWD(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error(e)
	}
	if e := absent.RunWithOverride(nil, "", nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error(e)
	}
	if _, e := absentState.Context(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error(e)
	}
	if absentState.Owner() != nil || state.Owner() != owner || absent.Owns(state) {
		t.Error("owner identity guard")
	}
	if _, _, e := absentCache.Get(nil, "A"); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error(e)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	e := owner.Run(canceled, func(scope *cooperative.Scope) error { _, e := ctx.GetCWD(scope); return e })
	if e == nil {
		t.Error("canceled admission accepted")
	}
}
