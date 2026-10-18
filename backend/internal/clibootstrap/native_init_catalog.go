package clibootstrap

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

type nativeCommandCatalogValue struct {
	remote      bool
	definitions []skillcommands.Definition
	plugins     []json.RawMessage
}

// This receipt is installed before the real factory starts. Constructor callers
// can await it synchronously, but later QueryEngine consumers retain its actual
// lifetime instead of recovering a Promise from PrintRegistry's string fields.
type nativeCommandCatalogLoad struct {
	cwd     string
	once    sync.Once
	done    chan struct{}
	value   nativeCommandCatalogValue
	failure error
	promise *ownerasync.OwnerFuture[nativeCommandCatalogValue]
}

type nativeQueryCatalog struct {
	owner         *cooperative.Owner
	native        *NativeBoundary
	skills        *skillcommands.Service
	workflows     *workflow.Service
	mu            sync.Mutex
	generation    uint64
	commands      map[string]*nativeCommandCatalogLoad
	slashes       map[*nativeCommandCatalogLoad]*ownerasync.OwnerFuture[[]json.RawMessage]
	settings      *NativeProcessSettings
	loader        *pluginregistry.Loader
	pluginSource  *pluginregistry.LoadFuture
	pluginPromise *ownerasync.OwnerFuture[pluginregistry.Result]
	fullSource    *pluginregistry.LoadFuture
	fullPromise   *ownerasync.OwnerFuture[pluginregistry.Result]
}

func (m *MainPrint) commandCatalogLoad() *nativeCommandCatalogLoad {
	r := &m.nativeQueryCatalog
	r.mu.Lock()
	defer r.mu.Unlock()
	generation := m.skillCommands.CacheGeneration()
	if r.commands == nil || generation != r.generation {
		r.commands = map[string]*nativeCommandCatalogLoad{}
		r.slashes = nil
		r.generation = generation
	}
	cwd := m.options.Native.CWD
	if m.state != nil {
		cwd = m.state.Snapshot().CWD
	}
	load := r.commands[cwd]
	if load == nil {
		load = &nativeCommandCatalogLoad{cwd: cwd, done: make(chan struct{})}
		r.commands[cwd] = load
	}
	if r.settings == nil {
		// Called by the actual native constructor after ensureSettings/skills.
		// Both objects retain this settings service's immutable loader identity.
		r.owner, r.native, r.skills, r.workflows = m.Owner, m.options.Native, m.skillCommands, m.workflowDefinitions
		r.settings, r.loader = m.nativeSettings, m.nativeSettings.plugins.loader
	}
	return load
}

func (m *MainPrint) invalidateNativeCommandCatalog() {
	r := &m.nativeQueryCatalog
	r.mu.Lock()
	r.commands, r.slashes = nil, nil
	r.mu.Unlock()
}

func (load *nativeCommandCatalogLoad) ready() (nativeCommandCatalogValue, error, bool) {
	select {
	case <-load.done:
		return load.value, load.failure, true
	default:
		return nativeCommandCatalogValue{}, nil, false
	}
}

func (m *MainPrint) runCommandCatalogLoad(ctx context.Context, load *nativeCommandCatalogLoad, scopes ...*cooperative.Scope) (nativeCommandCatalogValue, error) {
	start := false
	load.once.Do(func() { start = true })
	if start {
		load.value, load.failure = m.loadNativeCommandDefinitions(ctx, load.cwd, scopes...)
		close(load.done)
	}
	select {
	case <-load.done:
		return load.value, load.failure
	case <-ctx.Done():
		return nativeCommandCatalogValue{}, context.Cause(ctx)
	}
}

func (m *MainPrint) beginCommandCatalogLoad(scope *cooperative.Scope, load *nativeCommandCatalogLoad) (*ownerasync.OwnerFuture[nativeCommandCatalogValue], error) {
	if !scope.BelongsTo(m.Owner) || load == nil {
		return nil, cooperative.ErrRestricted
	}
	if load.promise != nil {
		return load.promise, nil
	}
	if value, failure, ready := load.ready(); ready {
		promise, err := ownerasync.NewOwnerFuture[nativeCommandCatalogValue](m.Owner)
		if err != nil {
			return nil, err
		}
		load.promise = promise
		if failure != nil {
			err = promise.Reject(scope, failure)
		} else {
			err = promise.Resolve(scope, value)
		}
		return promise, err
	}
	promise, err := nativeSkillWork(m, scope, func() (nativeCommandCatalogValue, error) {
		return m.runCommandCatalogLoad(context.Background(), load)
	})
	if err == nil {
		load.promise = promise
	}
	return promise, err
}

// nativeCommandDefinitions is the real getCommands consumer. The expensive
// source load is memoized; dynamic skills and MCP publication remain live.
func (m *MainPrint) nativeCommandDefinitions(ctx context.Context, scopes ...*cooperative.Scope) ([]skillcommands.Definition, []json.RawMessage, error) {
	if m.nativeSettings == nil || m.skillCommands == nil || len(scopes) > 1 || m.nativeQueryCatalog.owner != nil && !m.nativeInitCatalogBound() {
		return nil, nil, cooperative.ErrRestricted
	}
	load := m.commandCatalogLoad()
	var value nativeCommandCatalogValue
	var err error
	if len(scopes) == 1 {
		if !scopes[0].BelongsTo(m.Owner) {
			return nil, nil, cooperative.ErrRestricted
		}
		pending, e := m.beginCommandCatalogLoad(scopes[0], load)
		if e != nil {
			return nil, nil, e
		}
		value, err = pending.Await(scopes[0])
	} else {
		value, err = m.runCommandCatalogLoad(ctx, load)
	}
	if err != nil {
		return nil, nil, err
	}
	var definitions []skillcommands.Definition
	if len(scopes) == 1 {
		definitions, err = m.projectCommandCatalog(scopes[0], value)
	} else {
		// getCommands projects its completed immutable catalog in a short
		// read continuation; this does not admit another ASK while one waits.
		var receipt *cooperative.Receipt
		receipt, err = m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			projected, failure := m.projectCommandCatalog(scope, value)
			definitions = projected
			return failure
		})
		if err == nil {
			err = receipt.Wait()
		}
	}
	if err != nil {
		return nil, nil, err
	}
	if len(scopes) == 1 && m.mcpTools != nil {
		prompts, e := m.mcpTools.PromptCommands(scopes[0])
		if e != nil {
			return nil, nil, e
		}
		definitions = append(definitions, prompts...)
	}
	return definitions, append([]json.RawMessage{}, value.plugins...), nil
}

func (m *MainPrint) projectCommandCatalog(scope *cooperative.Scope, value nativeCommandCatalogValue) ([]skillcommands.Definition, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if value.remote {
		return []skillcommands.Definition{}, nil
	}
	base := []skillcommands.Definition{}
	seen := map[string]bool{}
	for _, definition := range value.definitions {
		enabled, err := definition.IsEnabled(scope)
		if err != nil {
			return nil, err
		}
		if !enabled {
			continue
		}
		base = append(base, definition)
		seen[definition.Name()] = true
	}
	dynamic := []skillcommands.Definition{}
	for _, definition := range m.skillCommands.Dynamic() {
		enabled, err := definition.IsEnabled(scope)
		if err != nil {
			return nil, err
		}
		if !enabled {
			continue
		}
		if !seen[definition.Name()] {
			dynamic = append(dynamic, definition)
			seen[definition.Name()] = true
		}
	}
	at := len(base)
	for i, definition := range base {
		if definition.Metadata().String("source") == "builtin" {
			at = i
			break
		}
	}
	result := append([]skillcommands.Definition{}, base[:at]...)
	result = append(result, dynamic...)
	return append(result, base[at:]...), nil
}

func (m *MainPrint) beginNativeSlashCommandSkills(scope *cooperative.Scope) (*ownerasync.OwnerFuture[[]json.RawMessage], error) {
	if !scope.BelongsTo(m.Owner) || !m.nativeInitCatalogBound() {
		return nil, cooperative.ErrRestricted
	}
	load := m.commandCatalogLoad()
	r := &m.nativeQueryCatalog
	r.mu.Lock()
	cached := r.slashes[load]
	r.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	// getCommands is async even when loadAllCommands has already completed.
	commands, err := ownerasync.NewOwnerFuture[[]skillcommands.Definition](m.Owner)
	if err != nil {
		return nil, err
	}
	all, err := m.beginCommandCatalogLoad(scope, load)
	if err != nil {
		return nil, err
	}
	_, err = all.Then(scope, func(next *cooperative.Scope, value nativeCommandCatalogValue, failure error) error {
		if failure != nil {
			return commands.Reject(next, failure)
		}
		definitions, err := m.projectCommandCatalog(next, value)
		if err != nil {
			return commands.Reject(next, err)
		}
		return commands.Resolve(next, definitions)
	})
	if err != nil {
		return nil, err
	}
	skills, err := ownerasync.NewOwnerFuture[[]json.RawMessage](m.Owner)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.slashes == nil {
		r.slashes = map[*nativeCommandCatalogLoad]*ownerasync.OwnerFuture[[]json.RawMessage]{}
	}
	r.slashes[load] = skills
	r.mu.Unlock()
	_, err = commands.Then(scope, func(next *cooperative.Scope, definitions []skillcommands.Definition, failure error) error {
		// The original getSlashCommandToolSkills catches a failed command load.
		if failure != nil {
			_ = m.nativeLogError(context.WithValue(context.Background(), nativeOpenAIAuthScopeKey{}, next), failure)
			if m.errorLogSink != nil {
				_ = m.errorLogSink.Debug("Returning empty skills array due to load failure", "DEBUG")
			}
			return skills.Resolve(next, []json.RawMessage{})
		}
		return skills.Resolve(next, sdkhost.NativeSlashCommandSkillNames(definitions))
	})
	return skills, err
}

func (m *MainPrint) beginNativePluginCatalog(scope *cooperative.Scope) (*ownerasync.OwnerFuture[pluginregistry.Result], error) {
	if !scope.BelongsTo(m.Owner) || !m.nativeInitCatalogBound() {
		return nil, cooperative.ErrRestricted
	}
	r := &m.nativeQueryCatalog
	if r.settings != m.nativeSettings || r.loader == nil || !r.loader.SharesSettingsState(m.settingState) {
		return nil, cooperative.ErrRestricted
	}
	producer := r.loader.CachedProducer()
	if r.pluginSource == producer && r.pluginPromise != nil {
		return r.pluginPromise, nil
	}
	var promise *ownerasync.OwnerFuture[pluginregistry.Result]
	var err error
	if _, _, ready := producer.Ready(); ready {
		promise, err = m.bridgeNativePluginProducer(scope, producer)
	} else if full := producer.DelegatedProducer(); full != nil {
		if r.fullSource != full || r.fullPromise == nil {
			r.fullPromise, err = m.bridgeNativePluginProducer(scope, full)
			if err != nil {
				return nil, err
			}
			r.fullSource = full
		}
		promise, err = ownerasync.NewOwnerFuture[pluginregistry.Result](m.Owner)
		if err == nil {
			err = promise.ResolveFrom(scope, r.fullPromise)
		}
	} else {
		promise, err = m.bridgeNativePluginProducer(scope, producer)
	}
	if err != nil {
		return nil, err
	}
	r.pluginSource, r.pluginPromise = producer, promise
	return promise, nil
}

func (m *MainPrint) bridgeNativePluginProducer(scope *cooperative.Scope, producer *pluginregistry.LoadFuture) (*ownerasync.OwnerFuture[pluginregistry.Result], error) {
	if value, failure, ready := producer.Ready(); ready {
		promise, err := ownerasync.NewOwnerFuture[pluginregistry.Result](m.Owner)
		if err != nil {
			return nil, err
		}
		if failure != nil {
			err = promise.Reject(scope, failure)
		} else {
			err = promise.Resolve(scope, value)
		}
		return promise, err
	}
	return nativeCommandSourceWork(m, scope, func() (pluginregistry.Result, error) {
		return m.nativeSettings.loadNativePluginProducer(context.Background(), producer)
	})
}

func (m *MainPrint) beginNativeInitCatalog(scope *cooperative.Scope) (*ownerasync.OwnerFuture[sdkhost.NativeInitCatalog], error) {
	if !scope.BelongsTo(m.Owner) || !m.nativeInitCatalogBound() {
		return nil, cooperative.ErrRestricted
	}
	// QueryEngine evaluates this array left-to-right, starts both actual memo
	// producers, and only then registers Promise.all's reactions.
	skills, err := m.beginNativeSlashCommandSkills(scope)
	if err != nil {
		return nil, err
	}
	plugins, err := m.beginNativePluginCatalog(scope)
	if err != nil {
		return nil, err
	}
	joined, err := ownerasync.NewOwnerFuture[sdkhost.NativeInitCatalog](m.Owner)
	if err != nil {
		return nil, err
	}
	value := sdkhost.NativeInitCatalog{}
	remaining := 2
	finish := func(next *cooperative.Scope, failure error) error {
		if failure != nil {
			return joined.Reject(next, failure)
		}
		remaining--
		if remaining == 0 {
			return joined.Resolve(next, value)
		}
		return nil
	}
	_, err = skills.Then(scope, func(next *cooperative.Scope, names []json.RawMessage, failure error) error {
		value.Skills = names
		return finish(next, failure)
	})
	if err != nil {
		return nil, err
	}
	_, err = plugins.Then(scope, func(next *cooperative.Scope, result pluginregistry.Result, failure error) error {
		value.Plugins = []json.RawMessage{}
		for _, plugin := range result.Enabled {
			value.Plugins = append(value.Plugins, rawMain(map[string]string{"name": plugin.Name, "path": plugin.Path, "source": plugin.Source}))
		}
		return finish(next, failure)
	})
	return joined, err
}

// Finite factories retain their original synchronous metadata path. Only this
// native factory's actual settings/loader/services can supply the init join.
func (m *MainPrint) nativeInitCatalogProducer() func(*cooperative.Scope) (*ownerasync.OwnerFuture[sdkhost.NativeInitCatalog], error) {
	if m.options.Native == nil || !m.options.Native.NativeProcess || m.nativeSettings == nil || m.skillCommands == nil {
		return nil
	}
	return m.beginNativeInitCatalog
}

func (m *MainPrint) nativeInitCatalogBound() bool {
	r := &m.nativeQueryCatalog
	return r.owner != nil && m.Owner == r.owner && m.options.Native == r.native && r.native != nil && r.native.NativeProcess &&
		m.nativeSettings == r.settings && m.skillCommands == r.skills && m.workflowDefinitions == r.workflows &&
		r.loader != nil && r.loader.SharesSettingsState(m.settingState)
}
