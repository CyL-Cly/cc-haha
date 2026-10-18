package clibootstrap

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginzip"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type nativeMarketplaceInstallation struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}
type nativePluginInstallation struct {
	mu                 sync.Mutex
	cancel             context.CancelFunc
	done               chan struct{}
	marketplaces       []nativeMarketplaceInstallation
	needsRefresh       bool
	syncWait           bool
	err                error
	marketplaceEnabled bool
	interactive        bool // immutable startup branch, sampled on the Owner before launch
	flaggedPlugins     []string
	initialErrors      []protocol.JSONObject
	subscribers        map[*nativePluginInstallationSubscription]bool
	version            uint64
}

type nativePluginInstallationSubscription struct {
	mu      sync.Mutex
	active  bool
	version uint64
	changed func(protocol.JSONObject)
}

func (state *nativePluginInstallation) snapshotLocked() protocol.JSONObject {
	markets := append([]nativeMarketplaceInstallation{}, state.marketplaces...)
	return protocol.NewJSONObject(protocol.JSONField{Name: "marketplaces", Value: rawMain(markets)}, protocol.JSONField{Name: "plugins", Value: rawMain([]any{})}, protocol.JSONField{Name: "needsRefresh", Value: rawMain(state.needsRefresh)}, protocol.JSONField{Name: "flaggedPlugins", Value: rawMain(append([]string{}, state.flaggedPlugins...))}, protocol.JSONField{Name: "initialErrors", Value: rawMain(append([]protocol.JSONObject{}, state.initialErrors...))})
}

func (state *nativePluginInstallation) publish() {
	state.mu.Lock()
	state.version++
	version, snapshot := state.version, state.snapshotLocked()
	subscribers := []*nativePluginInstallationSubscription{}
	for subscriber := range state.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	state.mu.Unlock()
	for _, subscriber := range subscribers {
		subscriber.mu.Lock()
		if subscriber.active && version > subscriber.version {
			subscriber.version = version
			subscriber.changed(snapshot.Clone())
		}
		subscriber.mu.Unlock()
	}
}

func (m *MainPrint) SubscribeNativePluginInstallation(ctx context.Context, changed func(protocol.JSONObject)) (protocol.JSONObject, func(), error) {
	if err := ctx.Err(); err != nil {
		return protocol.JSONObject{}, nil, err
	}
	state := m.nativePluginInstallation
	if state == nil || changed == nil {
		current, err := m.NativePluginInstallationStatus(ctx)
		return current, func() {}, err
	}
	state.mu.Lock()
	if state.subscribers == nil {
		state.subscribers = map[*nativePluginInstallationSubscription]bool{}
	}
	subscriber := &nativePluginInstallationSubscription{active: true, version: state.version, changed: changed}
	state.subscribers[subscriber] = true
	current := state.snapshotLocked()
	state.mu.Unlock()
	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			state.mu.Lock()
			delete(state.subscribers, subscriber)
			state.mu.Unlock()
			subscriber.mu.Lock()
			subscriber.active = false
			subscriber.mu.Unlock()
		})
	}
	stop := context.AfterFunc(ctx, unsubscribe)
	return current, func() { stop(); unsubscribe() }, nil
}

// NativePluginInstallationStatus reads the process's real startup producer.
// It never performs a fetch or infers marketplace failure from plugin errors.
func (m *MainPrint) NativePluginInstallationStatus(ctx context.Context) (protocol.JSONObject, error) {
	if err := ctx.Err(); err != nil {
		return protocol.JSONObject{}, err
	}
	state := m.nativePluginInstallation
	if state != nil {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.snapshotLocked(), nil
	}
	return (&nativePluginInstallation{}).snapshotLocked(), nil
}

// Source's remove-extra-marketplace action retires the current row. A later
// progress reaction maps the remaining array and cannot recreate that row.
func (m *MainPrint) RetireNativePluginMarketplaceInstallation(name string) {
	if state := m.nativePluginInstallation; state != nil {
		state.mu.Lock()
		remaining := state.marketplaces[:0]
		for _, marketplace := range state.marketplaces {
			if marketplace.Name != name {
				remaining = append(remaining, marketplace)
			}
		}
		state.marketplaces = remaining
		state.mu.Unlock()
		state.publish()
	}
}

func (m *MainPrint) beginNativePluginInstallation(scope *cooperative.Scope) error {
	if m.nativePluginInstallation != nil || !m.options.Native.NativeProcess || m.nativeSettings == nil || m.agentChild || m.options.DetachedAgent != nil {
		return nil
	}
	interactive := m.options.Interactive
	marketplaceEnabled := true
	if interactive {
		if !m.options.Arguments.Continue && (m.options.Arguments.Teleport != "" || m.options.Arguments.TeleportWithoutValue || m.options.Arguments.Remote != nil) {
			return nil
		}
		trusted, err := m.nativeGoalTrust(scope)
		if err != nil {
			return err
		}
		marketplaceEnabled = trusted
	} else if nativeEnvTruthy(m.options.Native.Environment["CLAUDE_CODE_SIMPLE"]) {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	state := &nativePluginInstallation{cancel: cancel, done: make(chan struct{}), marketplaces: []nativeMarketplaceInstallation{}, marketplaceEnabled: marketplaceEnabled, interactive: interactive, syncWait: !interactive && nativeEnvTruthy(m.options.Native.Environment["CLAUDE_CODE_SYNC_PLUGIN_INSTALL"])}
	m.nativePluginInstallation = state
	go func() {
		defer close(state.done)
		mountDone := make(chan error, 1)
		if state.interactive {
			go func() { mountDone <- m.runNativeInteractivePluginMount(ctx, state) }()
		} else {
			mountDone <- nil
		}
		var err error
		if !state.interactive && m.remoteManaged != nil {
			err = m.remoteManaged.waitOffOwner(ctx)
		}
		if err == nil && state.marketplaceEnabled {
			err = m.runNativePluginInstallation(ctx, state)
		}
		mountError := <-mountDone
		if err == nil {
			err = mountError
		}
		state.mu.Lock()
		state.err = err
		state.mu.Unlock()
	}()
	return nil
}

func (m *MainPrint) runNativePluginInstallation(ctx context.Context, state *nativePluginInstallation) error {
	jobs, err := newPluginCLIJobs(ctx, m.options.Native)
	if err != nil {
		return err
	}
	defer jobs.Join()
	manager, err := m.nativeSettings.pluginCLIMarketplaces(jobs)
	if err != nil {
		return err
	}
	seedChanged, err := manager.RegisterSeeds(ctx)
	if err != nil {
		return err
	}
	if seedChanged {
		if err = m.nativeSettings.pluginCLIClear(ctx); err != nil {
			return err
		}
		state.mu.Lock()
		state.needsRefresh = true
		state.mu.Unlock()
		state.publish()
	}
	declared, err := m.nativeSettings.declaredPluginMarketplaces(ctx)
	if err != nil {
		return err
	}
	known, err := manager.Known(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		known = protocol.NewJSONObject()
	}
	options := pluginsapi.ReconcileOptions{}
	if state.interactive {
		diff := pluginsapi.DiffMarketplaces(declared, known, m.options.Native.CWD, m.nativeSettings.plugins.roots)
		state.mu.Lock()
		for _, name := range diff.Missing {
			state.marketplaces = append(state.marketplaces, nativeMarketplaceInstallation{Name: name, Status: "pending"})
		}
		for _, change := range diff.SourceChanged {
			state.marketplaces = append(state.marketplaces, nativeMarketplaceInstallation{Name: change.Name, Status: "pending"})
		}
		nothing := len(state.marketplaces) == 0
		state.mu.Unlock()
		state.publish()
		if nothing {
			return nil
		}
		options.OnProgress = func(event pluginsapi.ReconcileProgress) {
			state.mu.Lock()
			for i := range state.marketplaces {
				if state.marketplaces[i].Name == event.Name {
					state.marketplaces[i].Status = event.Type
					state.marketplaces[i].Error = event.Error
				}
			}
			state.mu.Unlock()
			state.publish()
		}
	} else if m.nativeSettings.plugins.archive.CacheEnabled() {
		path := m.nativeSettings.plugins.archive.CachePath()
		for _, directory := range []string{"marketplaces", "plugins"} {
			if err := os.MkdirAll(filepath.Join(path, directory), 0777); err != nil {
				return err
			}
		}
		options.Skip = func(_ string, source protocol.JSONObject) bool {
			return !pluginzip.SupportedMarketplaceSource(source.String("source"))
		}
	}
	result, err := manager.Reconcile(ctx, declared, m.options.Native.CWD, options)
	if err != nil {
		return err
	}
	if !state.interactive {
		if err = manager.SyncZipMarketplaces(ctx, m.nativeSettings.plugins.archive); err != nil {
			return err
		}
	}
	changed := seedChanged || len(result.Installed)+len(result.Updated) > 0
	if !state.interactive {
		operations, err := m.nativeSettings.pluginCLIOperations(m.pluginOptions, jobs)
		if err != nil {
			return err
		}
		delisted, err := manager.EnforceDelisting(ctx, operations, m.options.Native.Now())
		if err != nil {
			return err
		}
		changed = changed || len(delisted) > 0
	}
	if !changed {
		return nil
	}
	if err = m.nativeSettings.pluginCLIClear(ctx); err != nil {
		return err
	}
	if state.interactive && len(result.Installed) == 0 {
		state.mu.Lock()
		state.needsRefresh = true
		state.mu.Unlock()
		state.publish()
		return nil
	}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		_, err := m.reloadComponentsOnOwner(ctx, scope)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	state.mu.Lock()
	state.needsRefresh = err != nil
	state.mu.Unlock()
	state.publish()
	return err
}

func (m *MainPrint) closeNativePluginInstallation() {
	if state := m.nativePluginInstallation; state != nil {
		state.cancel()
		<-state.done
	}
}

func (m *MainPrint) cancelNativePluginInstallation() {
	if state := m.nativePluginInstallation; state != nil {
		state.cancel()
	}
}

func (m *MainPrint) awaitNativePluginInstallation(scope *cooperative.Scope, ctx context.Context) error {
	state := m.nativePluginInstallation
	if state == nil || !state.syncWait {
		return nil
	}
	state.syncWait = false
	// This is the headless input lifetime, not an interactive model turn.
	// NativeOwnedWork joins its producer on abort, so the physical install
	// must receive cancellation before that same producer can finish.
	stopCancel := context.AfterFunc(ctx, state.cancel)
	defer stopCancel()
	future, err := nativeSkillWork(m, scope, func() (struct{}, error) {
		text := protocol.TrimJavaScriptString(m.options.Native.Environment["CLAUDE_CODE_SYNC_PLUGIN_INSTALL_TIMEOUT_MS"])
		end := 0
		if len(text) > 0 && (text[0] == '+' || text[0] == '-') {
			end = 1
		}
		start := end
		for end < len(text) && text[end] >= '0' && text[end] <= '9' {
			end++
		}
		milliseconds := float64(0)
		if end > start {
			milliseconds, _ = strconv.ParseFloat(text[:end], 64)
		}
		if milliseconds > 0 {
			// Bun's setTimeout uses the same signed 32-bit timer budget.
			if milliseconds > 2147483647 {
				milliseconds = 1
			}
			timer := time.NewTimer(time.Duration(milliseconds) * time.Millisecond)
			defer timer.Stop()
			select {
			case <-state.done:
			case <-timer.C:
			}
		} else {
			<-state.done
		}
		return struct{}{}, nil
	})
	if err != nil {
		return err
	}
	_, err = future.Await(scope)
	if err != nil {
		return err
	}
	_, err = m.reloadComponentsOnOwner(context.Background(), scope)
	return err
}
