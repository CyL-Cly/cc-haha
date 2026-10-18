package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

func (m *MainPrint) nativeShellResolverOnOwner(scope *cooperative.Scope) (*windowshell.Resolver, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	resolver := m.options.NativeShells
	if resolver == nil {
		if !windowshell.IsWindows(m.options.Platform) {
			return nil, nil
		}
		env, err := m.nativeProcessEnvironment(scope)
		if err != nil {
			return nil, err
		}
		resolver = windowshell.NewNative(m.options.Platform, m.state.Snapshot().CWD, env)
		m.options.NativeShells = resolver
	}
	if _, warmed := resolver.Snapshot(); !warmed {
		probeContext, cancel := context.WithCancel(context.Background())
		defer cancel()
		future, err := nativeSkillWork(m, scope, func() (windowshell.Snapshot, error) {
			return resolver.Warm(probeContext), nil
		})
		if err != nil {
			return nil, err
		}
		if _, err = future.Await(scope); err != nil {
			return nil, err
		}
	}
	if windowshell.IsWindows(m.options.Platform) {
		view, warmed := resolver.Snapshot()
		if warmed && view.GitBash != "" {
			if err := m.environment.SetProperty(quoteMain("SHELL"), quoteMain(view.GitBash)); err != nil {
				return nil, err
			}
		}
	}
	return resolver, nil
}

func (m *MainPrint) nativeShellAvailability(scope *cooperative.Scope) (windowshell.Availability, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return windowshell.Availability{}, cooperative.ErrRestricted
	}
	if !windowshell.IsWindows(m.options.Platform) && (m.options.NativeShells == nil || !windowshell.IsWindows(m.options.NativeShells.Platform())) {
		return windowshell.Availability{Bash: true}, nil
	}
	if m.options.NativeShells == nil {
		return windowshell.Availability{}, mainCap("owned Windows shell availability")
	}
	view, warmed := m.options.NativeShells.Snapshot()
	if !warmed {
		return windowshell.Availability{}, mainCap("Windows shell discovery before prompt invocation")
	}
	return view.Availability, nil
}
