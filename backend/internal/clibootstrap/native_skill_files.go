package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// Native producers take immutable paths, never an owner Scope. The consumed
// trigger set, activation and Promise reactions remain on this Harness.
func nativeSkillWork[T any](m *MainPrint, scope *cooperative.Scope, work func() (T, error)) (*ownerasync.OwnerFuture[T], error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	binding := m.commandBinding
	var e error
	if binding == nil {
		if m.Controller == nil {
			return nil, cooperative.ErrRestricted
		}
		binding, e = m.Controller.BorrowNativeCopyBinding(m.Owner)
	}
	if e != nil {
		return nil, e
	}
	return nativeOwnedWork(m.Owner, binding, scope, work)
}

func (m *MainPrint) nativeFileSkills(_ context.Context, scope *cooperative.Scope, path string) (*ownerasync.OwnerFuture[struct{}], error) {
	if m.skillCommands == nil || !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	current, e := m.state.Context(scope)
	if e != nil {
		return nil, e
	}
	cwd, e := current.GetCWD(scope)
	if e != nil {
		return nil, e
	}
	service := m.skillCommands
	discovered, e := nativeSkillWork(m, scope, func() ([]string, error) { return service.Discover(context.Background(), []string{path}, cwd) })
	if e != nil {
		return nil, e
	}
	result, e := ownerasync.NewOwnerFuture[struct{}](m.Owner)
	if e != nil {
		return nil, e
	}
	_, e = discovered.Then(scope, func(next *cooperative.Scope, dirs []string, failure error) error {
		if failure != nil {
			return result.Reject(next, failure)
		}
		if len(dirs) > 0 {
			for _, dir := range dirs {
				present := false
				for _, old := range m.dynamicSkillDirTriggers {
					present = present || dir == old
				}
				if !present {
					m.dynamicSkillDirTriggers = append(m.dynamicSkillDirTriggers, dir)
				}
			}
			loading, e := nativeSkillWork(m, next, func() (struct{}, error) { return struct{}{}, service.AddDirectories(context.Background(), dirs) })
			if e != nil {
				return result.Reject(next, e)
			}
			// Source catches this detached promise; its native IO is still owned
			// until delivery, while the file operation proceeds without waiting.
			if _, e = loading.Then(next, func(*cooperative.Scope, struct{}, error) error { return nil }); e != nil {
				return result.Reject(next, e)
			}
		}
		if _, e := service.ActivateConditional([]string{path}, cwd); e != nil {
			return result.Reject(next, e)
		}
		return result.Resolve(next, struct{}{})
	})
	if e != nil {
		return nil, e
	}
	return result, nil
}
