package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativemodule"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
)

const nativePluginModuleKey = "cli/handlers/plugins"

// This is the actual non-model CLI handler namespace, not a settings record or
// a synthetic filesystem resource. Go already links its handler code. Source's
// Commander action still awaits its dynamic-import protocol before invoking it.
type nativePluginNamespace struct {
	command *MainCommand
	binding *nativeCommandInitBinding
}

func (namespace *nativePluginNamespace) check(scope *cooperative.Scope) error {
	if namespace == nil || namespace.command == nil || namespace.binding == nil {
		return cooperative.ErrRestricted
	}
	m := namespace.command
	if !scope.BelongsTo(m.Owner) || namespace.binding != m.nativeCommandInit || !m.ownsNativeCommandFactory() {
		return cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return err
	}
	m.nativeAPIPreconnect.life.mu.Lock()
	closed := m.nativeAPIPreconnect.life.closed
	m.nativeAPIPreconnect.life.mu.Unlock()
	if closed {
		return cooperative.ErrClosed
	}
	return nil
}

func (m *MainCommand) importNativePluginNamespace(scope *cooperative.Scope) (*ownerasync.OwnerFuture[*nativePluginNamespace], error) {
	if !m.ownsNativeCommandFactory() || !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if m.nativePluginModule == nil {
		namespace := &nativePluginNamespace{command: m, binding: m.nativeCommandInit}
		entry, err := nativemodule.NewPrelinked(m.Owner, scope, nativePluginModuleKey, namespace, func(next *cooperative.Scope, key string, got *nativePluginNamespace) error {
			if key != nativePluginModuleKey || got != namespace {
				return cooperative.ErrRestricted
			}
			return namespace.check(next)
		})
		if err != nil {
			return nil, err
		}
		m.nativePluginModule = entry
	}
	return m.nativePluginModule.Import(scope)
}

func (namespace *nativePluginNamespace) invoke(ctx context.Context, scope *cooperative.Scope, command plugincli.Command) (plugincli.Result, error) {
	if err := namespace.check(scope); err != nil {
		return plugincli.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return plugincli.Result{}, err
	}
	return namespace.command.pluginCommandHandler(ctx, scope, command)
}
