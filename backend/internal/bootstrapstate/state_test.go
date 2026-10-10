package bootstrapstate

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func textPointer(value string) *string { return &value }
func TestStateSwitchAtomicSameSIDAndCache(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	state, _ := New(owner, "A", "/initial", func() (string, error) { return "B", nil })
	run(t, owner, func(scope *cooperative.Scope) error {
		cache, _ := state.PlanSlugCache(scope)
		_ = cache.Set(scope, "A", "old")
		_ = cache.Set(scope, "B", "future")
		calls := 0
		_, _ = state.OnSessionSwitch(scope, NewListener(func(next *cooperative.Scope, id string) error {
			calls++
			value := state.Snapshot()
			entries, _ := cache.Entries(next)
			if value.SessionID != id || value.SessionProjectDir == nil || *value.SessionProjectDir != "project-e\u0301" || !reflect.DeepEqual(entries, []SlugEntry{{"B", "future"}}) {
				return fmt.Errorf("observer ran before atomic switch/cache invalidation: %#v %#v", value, entries)
			}
			return nil
		}))
		if err := state.SwitchSession(scope, "A", textPointer("project-e\u0301")); err != nil {
			return err
		}
		if calls != 1 {
			return fmt.Errorf("same SID notification removed: %d calls", calls)
		}
		current, _ := state.PlanSlugCache(scope)
		if current != cache {
			t.Error("Map identity replaced")
		}
		copy := state.Snapshot()
		*copy.SessionProjectDir = "mutated"
		if *state.Snapshot().SessionProjectDir == "mutated" {
			t.Error("snapshot escaped mutable state")
		}
		return nil
	})
}
func TestRegenerateRetainsParentAndDoesNotEmit(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	ids := []string{"B", "C"}
	state, _ := New(owner, "A", "/initial", func() (string, error) { value := ids[0]; ids = ids[1:]; return value, nil })
	run(t, owner, func(scope *cooperative.Scope) error {
		cache, _ := state.PlanSlugCache(scope)
		_ = cache.Set(scope, "A", "old")
		_ = cache.Set(scope, "other", "keep")
		calls := 0
		_, _ = state.OnSessionSwitch(scope, NewListener(func(*cooperative.Scope, string) error { calls++; return nil }))
		id, err := state.RegenerateSessionID(scope, true)
		if err != nil {
			return err
		}
		if id != "B" {
			t.Error(id)
		}
		_ = cache.Set(scope, "B", "new-old")
		id, err = state.RegenerateSessionID(scope, false)
		if err != nil {
			return err
		}
		value := state.Snapshot()
		entries, _ := cache.Entries(scope)
		if id != "C" || value.ParentSessionID == nil || *value.ParentSessionID != "A" || value.SessionProjectDir != nil || calls != 0 || !reflect.DeepEqual(entries, []SlugEntry{{"other", "keep"}}) {
			t.Error(value, entries, calls)
		}
		return nil
	})
}
func TestRegenerateUUIDFailurePreservesEarlierMutations(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	original := errors.New("entropy failed")
	state, _ := New(owner, "A", "/initial", func() (string, error) { return "", original })
	run(t, owner, func(scope *cooperative.Scope) error {
		_ = state.SwitchSession(scope, "A", textPointer("project"))
		cache, _ := state.PlanSlugCache(scope)
		_ = cache.Set(scope, "A", "old")
		_, err := state.RegenerateSessionID(scope, true)
		if err != original {
			t.Error(err)
		}
		value := state.Snapshot()
		entries, _ := cache.Entries(scope)
		if value.SessionID != "A" || value.ParentSessionID == nil || *value.ParentSessionID != "A" || *value.SessionProjectDir != "project" || len(entries) != 0 {
			t.Error("UUID throw rolled back original prior mutations", value, entries)
		}
		return nil
	})
}
func TestSwitchErrorDoesNotRollbackState(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	state, _ := New(owner, "A", "/initial", func() (string, error) { return "C", nil })
	original := errors.New("listener")
	run(t, owner, func(scope *cooperative.Scope) error {
		_, _ = state.OnSessionSwitch(scope, NewListener(func(*cooperative.Scope, string) error { return original }))
		_, _ = state.OnSessionSwitch(scope, NewListener(func(*cooperative.Scope, string) error { t.Error("after throw"); return nil }))
		if err := state.SwitchSession(scope, "B", nil); err != original || state.SessionID() != "B" {
			t.Error("switch failure lost state/error", err)
		}
		return nil
	})
}
func TestStateIndependentNFCAndMapOrder(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	state, _ := New(owner, "A", "/initial-e\u0301", func() (string, error) { return "B", nil })
	run(t, owner, func(scope *cooperative.Scope) error {
		_ = state.SetOriginalCWD(scope, "/cwd-e\u0301")
		_ = state.SetProjectRoot(scope, "/project-o\u0308")
		_ = state.SetCWD(scope, "/shell-A\u030a")
		value := state.Snapshot()
		if value.OriginalCWD != "/cwd-é" || value.ProjectRoot != "/project-ö" || value.CWD != "/shell-Å" {
			t.Error(value)
		}
		cache, _ := state.PlanSlugCache(scope)
		_ = cache.Set(scope, "A", "one")
		_ = cache.Set(scope, "B", "two")
		_ = cache.Set(scope, "A", "overwritten")
		entries, _ := cache.Entries(scope)
		if !reflect.DeepEqual(entries, []SlugEntry{{"A", "overwritten"}, {"B", "two"}}) {
			t.Error(entries)
		}
		deleted, _ := cache.Delete(scope, "A")
		missing, _ := cache.Delete(scope, "missing")
		if !deleted || missing {
			t.Error(deleted, missing)
		}
		_ = cache.Set(scope, "A", "readded")
		entries, _ = cache.Entries(scope)
		if !reflect.DeepEqual(entries, []SlugEntry{{"B", "two"}, {"A", "readded"}}) {
			t.Error(entries)
		}
		_ = cache.Clear(scope)
		entries, _ = cache.Entries(scope)
		if len(entries) != 0 {
			t.Error(entries)
		}
		return nil
	})
}
func TestStateForeignOwnerAndUTF8Rejection(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	foreign := cooperative.New()
	defer foreign.Close()
	calls := 0
	state, _ := New(owner, "A", "/initial", func() (string, error) { calls++; return "B", nil })
	run(t, foreign, func(scope *cooperative.Scope) error {
		if err := state.SwitchSession(scope, "B", nil); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error(err)
		}
		if _, err := state.RegenerateSessionID(scope, true); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error(err)
		}
		return nil
	})
	run(t, owner, func(scope *cooperative.Scope) error {
		if err := state.SwitchSession(scope, string([]byte{0xff}), nil); !errors.Is(err, ErrStringDomain) {
			t.Error(err)
		}
		return nil
	})
	if calls != 0 || state.SessionID() != "A" {
		t.Error("rejected input mutated state")
	}
}
