package bootstrapstate

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// CWDContext is an explicit immutable async-context value. It does not mutate
// global CWD or own an Owner. Async descendants retain this token, while a
// default context reads the live State on every getCwd call.
type CWDContext struct {
	state    *State
	override *string
}

func (state *State) Context(scope *cooperative.Scope) (*CWDContext, error) {
	if state == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := state.CheckScope(scope); err != nil {
		return nil, err
	}
	return &CWDContext{state: state}, nil
}
func (cwd *CWDContext) Owns(state *State) bool { return cwd != nil && cwd.state == state }
func (cwd *CWDContext) GetCWD(scope *cooperative.Scope) (string, error) {
	if cwd == nil || cwd.state == nil {
		return "", cooperative.ErrRestricted
	}
	if err := cwd.state.CheckScope(scope); err != nil {
		return "", err
	}
	if cwd.override != nil {
		return *cwd.override, nil
	}
	return cwd.state.Snapshot().CWD, nil
}

// RunWithOverride retains Source's literal string (including NFD/empty), unlike
// State.SetCWD which normalizes NFC. Returning never restores or changes shared
// state: nested/sibling contexts are independent immutable values. The caller's
// original Future returned by its body can retain next after this frame returns.
func (cwd *CWDContext) RunWithOverride(scope *cooperative.Scope, value string, run func(*CWDContext) error) error {
	if cwd == nil || cwd.state == nil || run == nil {
		return cooperative.ErrRestricted
	}
	if err := cwd.state.CheckScope(scope); err != nil {
		return err
	}
	if err := valid(value); err != nil {
		return err
	}
	next := &CWDContext{state: cwd.state, override: &value}
	return run(next)
}

func (cache *PlanSlugs) Get(scope *cooperative.Scope, id string) (string, bool, error) {
	if cache == nil {
		return "", false, cooperative.ErrRestricted
	}
	if err := check(scope, cache.owner); err != nil {
		return "", false, err
	}
	if err := valid(id); err != nil {
		return "", false, err
	}
	for _, entry := range cache.entries {
		if entry.SessionID == id {
			return entry.Slug, true, nil
		}
	}
	return "", false, nil
}

// Owner is an immutable identity, not a new scheduler or permission to use a Scope.
func (state *State) Owner() *cooperative.Owner {
	if state == nil {
		return nil
	}
	return state.owner
}
