package clibootstrap

import (
	"context"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
)

func (m *MainPrint) beginNativeLocalCommandSource(scope *cooperative.Scope, ctx context.Context, cwd string) (*ownerasync.OwnerFuture[[]skillcommands.Definition], error) {
	if err := m.checkNativeCommandSources(scope); err != nil {
		return nil, err
	}
	producer := m.skillCommands.LocalProducer(cwd)
	if definitions, failure, ready := producer.Ready(); ready {
		promise, err := ownerasync.NewOwnerFuture[[]skillcommands.Definition](m.Owner)
		if err != nil {
			return nil, err
		}
		if failure != nil {
			err = promise.Reject(scope, failure)
		} else {
			err = promise.Resolve(scope, definitions)
		}
		return promise, err
	}
	return nativeCommandSourceWork(m, scope, func() ([]skillcommands.Definition, error) { return producer.Load(ctx) })
}

type nativePluginCommandBridge struct {
	producer *skillcommands.PluginDefinitionLoadFuture
	promise  *ownerasync.OwnerFuture[[]skillcommands.Definition]
}
type nativeCommandSourcesRuntime struct {
	plugins           [2]nativePluginCommandBridge
	mu                sync.Mutex
	closing           bool
	startupReferences ownerasync.NativeReferences
}

func (m *MainPrint) checkNativeCommandSources(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) || !m.nativeInitCatalogBound() {
		return cooperative.ErrRestricted
	}
	r := &m.nativeCommandSourcesRuntime
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return cooperative.ErrClosed
	}
	return nil
}

// Startup precedes Controller construction. Only this exact factory can admit
// those real sources; later calls use the original Writer's physical ledger.
func nativeCommandSourceWork[T any](m *MainPrint, scope *cooperative.Scope, work func() (T, error)) (*ownerasync.OwnerFuture[T], error) {
	if !scope.BelongsTo(m.Owner) || !m.nativeInitCatalogBound() {
		return nil, cooperative.ErrRestricted
	}
	r := &m.nativeCommandSourcesRuntime
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return nil, cooperative.ErrClosed
	}
	if m.Controller != nil {
		return nativeSkillWork(m, scope, work)
	}
	binding, err := r.startupReferences.Bind(m.Owner)
	if err != nil {
		return nil, err
	}
	return nativeOwnedWork(m.Owner, binding, scope, work)
}

func (m *MainPrint) joinNativeCommandSources() {
	r := &m.nativeCommandSourcesRuntime
	r.mu.Lock()
	r.closing = true
	r.mu.Unlock()
	for {
		count, idle := r.startupReferences.Snapshot()
		if count == 0 {
			return
		}
		<-idle
	}
}

func (m *MainPrint) beginNativePluginCommandSource(scope *cooperative.Scope, ctx context.Context, skills bool) (*ownerasync.OwnerFuture[[]skillcommands.Definition], error) {
	if err := m.checkNativeCommandSources(scope); err != nil {
		return nil, err
	}
	index := 0
	if skills {
		index = 1
	}
	producer := m.skillCommands.PluginDefinitionProducer(skills)
	bridge := &m.nativeCommandSourcesRuntime.plugins[index]
	if bridge.producer == producer && bridge.promise != nil {
		return bridge.promise, nil
	}
	out, err := ownerasync.NewOwnerFuture[[]skillcommands.Definition](m.Owner)
	if err != nil {
		return nil, err
	}
	bridge.producer, bridge.promise = producer, out
	if value, ready := producer.Ready(); ready {
		return out, out.Resolve(scope, value)
	}
	bare, _ := m.options.Native.nativeEnvironmentValue("CLAUDE_CODE_SIMPLE")
	if nativeEnvTruthy(bare) && len(m.options.Arguments.PluginDirectories) == 0 {
		// Original async getPluginCommands/getPluginSkills return [] before
		// consulting the marketplace catalog in bare mode without inline dirs.
		return out, out.Resolve(scope, producer.Load(ctx, nil))
	}
	catalog, err := m.beginNativePluginCatalog(scope)
	if err != nil {
		return nil, err
	}
	_, err = catalog.Then(scope, func(next *cooperative.Scope, result pluginregistry.Result, failure error) error {
		if failure != nil {
			return out.Reject(next, failure)
		}
		var loaded *ownerasync.OwnerFuture[[]skillcommands.Definition]
		var err error
		if len(result.Enabled) == 0 {
			// No plugin paths exist: run the real empty source body on Owner,
			// rather than dispatching native IO with nothing to read.
			loaded, err = ownerasync.NewOwnerFuture[[]skillcommands.Definition](m.Owner)
			if err == nil {
				err = loaded.Resolve(next, producer.Load(ctx, result.Enabled))
			}
		} else {
			loaded, err = nativeCommandSourceWork(m, next, func() ([]skillcommands.Definition, error) { return producer.Load(ctx, result.Enabled), nil })
		}
		if err != nil {
			return out.Reject(next, err)
		}
		// Original getPlugin* asynchronously returns the actual component
		// loader Promise after awaiting its shared plugin catalog.
		return out.ResolveFrom(next, loaded)
	})
	return out, err
}

// loadAllCommands evaluates getSkills (Local, then plugin Skills), plugin
// Commands, and Workflow in Source order. Its joins/reactions stay on Owner;
// actual pending filesystem producers keep their existing NativeBinding ledger.
func (m *MainPrint) beginNativeCommandSources(ctx context.Context, scope *cooperative.Scope, cwd string) (*nativeCommandSourcesLoad, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	local, err := m.beginNativeLocalCommandSource(scope, ctx, cwd)
	if err != nil {
		return nil, err
	}
	pluginSkills, err := m.beginNativePluginCommandSource(scope, ctx, true)
	if err != nil {
		return nil, err
	}
	pair, err := ownerasync.NewOwnerFuture[struct{}](m.Owner)
	if err != nil {
		return nil, err
	}
	var definitions, skillDefinitions, commandDefinitions, workflowDefinitions []skillcommands.Definition
	pairRemaining, pairSettled := 2, false
	pairFinish := func(next *cooperative.Scope, failure error) error {
		if pairSettled {
			return nil
		}
		if failure != nil {
			pairSettled = true
			return pair.Reject(next, failure)
		}
		pairRemaining--
		if pairRemaining == 0 {
			pairSettled = true
			return pair.Resolve(next, struct{}{})
		}
		return nil
	}
	_, err = local.Then(scope, func(next *cooperative.Scope, value []skillcommands.Definition, failure error) error {
		definitions = value
		return pairFinish(next, failure)
	})
	if err != nil {
		return nil, err
	}
	_, err = pluginSkills.Then(scope, func(next *cooperative.Scope, value []skillcommands.Definition, failure error) error {
		skillDefinitions = value
		return pairFinish(next, failure)
	})
	if err != nil {
		return nil, err
	}
	skills, err := ownerasync.NewOwnerFuture[struct{}](m.Owner)
	if err != nil {
		return nil, err
	}
	_, err = pair.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			return skills.Reject(next, failure)
		}
		return skills.Resolve(next, struct{}{})
	})
	if err != nil {
		return nil, err
	}
	pluginCommands, err := m.beginNativePluginCommandSource(scope, ctx, false)
	if err != nil {
		return nil, err
	}
	workflows, err := m.workflowDefinitions.BeginCommandsForDirectoryOnOwner(scope, cwd)
	if err != nil {
		return nil, err
	}
	catalogSource := m.nativeQueryCatalog.loader.CachedProducer()
	joined, err := ownerasync.NewOwnerFuture[struct{}](m.Owner)
	if err != nil {
		return nil, err
	}
	remaining, settled := 3, false
	finish := func(next *cooperative.Scope, failure error) error {
		if settled {
			return nil
		}
		if failure != nil {
			settled = true
			return joined.Reject(next, failure)
		}
		remaining--
		if remaining == 0 {
			settled = true
			return joined.Resolve(next, struct{}{})
		}
		return nil
	}
	_, err = skills.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error { return finish(next, failure) })
	if err != nil {
		return nil, err
	}
	_, err = pluginCommands.Then(scope, func(next *cooperative.Scope, value []skillcommands.Definition, failure error) error {
		commandDefinitions = value
		return finish(next, failure)
	})
	if err != nil {
		return nil, err
	}
	_, err = workflows.Then(scope, func(next *cooperative.Scope, commands []*workflowcommands.Command, failure error) error {
		if failure != nil {
			return finish(next, failure)
		}
		enabled := []*workflowcommands.Command{}
		for _, command := range commands {
			active, failure := command.IsEnabled(next)
			if failure != nil {
				return finish(next, failure)
			}
			if active {
				enabled = append(enabled, command)
			}
		}
		var projectionError error
		workflowDefinitions, projectionError = m.nativeWorkflowCommandDefinitionsFromCommands(next, enabled)
		return finish(next, projectionError)
	})
	if err != nil {
		return nil, err
	}
	return &nativeCommandSourcesLoad{done: joined, collect: func(next *cooperative.Scope) ([]skillcommands.Definition, pluginregistry.Result, error) {
		if !next.BelongsTo(m.Owner) {
			return nil, pluginregistry.Result{}, cooperative.ErrRestricted
		}
		// getPlugin* and enabled Workflow producers already joined this exact
		// catalog; bare disabled branches need not start an unrelated loader here.
		plugins, catalogError, ready := catalogSource.Ready()
		if ready && catalogError != nil {
			return nil, pluginregistry.Result{}, catalogError
		}
		if !ready {
			plugins = pluginregistry.Result{}
		}
		definitions = append(definitions, workflowDefinitions...)
		definitions = append(definitions, commandDefinitions...)
		definitions = append(definitions, skillDefinitions...)
		return definitions, plugins, nil
	}}, nil
}

// This state retains the actual source join and its Owner-only continuation,
// without keeping an independent Read frame parked while filesystem IO waits.
type nativeCommandSourcesLoad struct {
	done    *ownerasync.OwnerFuture[struct{}]
	collect func(*cooperative.Scope) ([]skillcommands.Definition, pluginregistry.Result, error)
}

func (m *MainPrint) nativeCommandSources(ctx context.Context, scope *cooperative.Scope, cwd string) ([]skillcommands.Definition, pluginregistry.Result, []skillcommands.Definition, error) {
	load, err := m.beginNativeCommandSources(ctx, scope, cwd)
	if err != nil {
		return nil, pluginregistry.Result{}, nil, err
	}
	// The scoped caller retains its original await reaction. The off-owner
	// caller instead registers this same continuation and waits outside Owner.
	if _, err = load.done.Await(scope); err != nil {
		return nil, pluginregistry.Result{}, nil, err
	}
	definitions, plugins, err := load.collect(scope)
	return definitions, plugins, nil, err
}
