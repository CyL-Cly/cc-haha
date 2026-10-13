package clibootstrap

import (
	"context"
	"fmt"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillwatcher"
)

type nativeSkillWatcher struct {
	watcher            *skillwatcher.Watcher
	unsubscribe        func()
	unsubscribeManaged func()
	managedContext     context.Context
	managedCancel      context.CancelFunc
	managedMu          sync.Mutex
	managedClosed      bool
	managedPending     sync.WaitGroup
	once               sync.Once
}

func (s *nativeSkillWatcher) close() {
	if s != nil {
		s.once.Do(func() {
			s.managedMu.Lock()
			s.managedClosed = true
			s.managedMu.Unlock()
			if s.unsubscribeManaged != nil {
				s.unsubscribeManaged()
			}
			if s.managedCancel != nil {
				s.managedCancel()
			}
			if s.unsubscribe != nil {
				s.unsubscribe()
			}
			s.watcher.Close()
			s.managedPending.Wait()
		})
	}
}
func (m *MainPrint) startNativeSkillWatcher() error {
	if m.skillWatcher != nil || m.agentChild || m.nativeSettings == nil || envTruthyNative(m.options.Native.Environment["CLAUDE_CODE_SIMPLE"]) {
		return nil
	}
	watchOptions := skillwatcher.Options{Paths: m.skillCommands.WatchPaths(context.Background(), m.computerUseConfigPath()), ReconcileMissingPaths: true, Reload: m.reloadNativeSkillCommands, OnError: m.reportNativeSkillRefreshError}
	if m.options.ComputerUse != nil {
		watchOptions.Timing = m.options.ComputerUse.SkillWatchTiming
	}
	w, e := skillwatcher.Start(watchOptions)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.skillWatcher = &nativeSkillWatcher{watcher: w, unsubscribe: m.skillCommands.OnLoaded(w.NotifyDynamic), managedContext: ctx, managedCancel: cancel}
	m.skillWatcher.unsubscribeManaged = subscribeNativeSkillChanges(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], func() error { return m.skillWatcher.enqueueManagedRefresh(m) })
	return nil
}

func (m *MainPrint) reportNativeSkillRefreshError(err error) {
	m.outputMu.Lock()
	defer m.outputMu.Unlock()
	_, _ = fmt.Fprintf(m.options.Error, "Skill command refresh failed: %v\n", err)
}
func (m *MainPrint) reloadNativeSkillCommands(ctx context.Context, path string, dynamic bool) error {
	receipt, e := m.Owner.Submit(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if m.closed || m.shuttingDown {
			return cooperative.ErrClosed
		}
		if !dynamic {
			results, e := m.hook.ExecuteConfigChange(ctx, "skills", quoteMain(path), queryhooks.Context{Scope: scope})
			if e != nil {
				return e
			}
			if queryhooks.HasBlockingOutsideResult(results) {
				return nil
			}
			m.skillCommands.Clear()
			if e := m.workflowDefinitions.ClearCommandsOnOwner(scope); e != nil {
				return e
			}
			if e := m.resetNativeSkillListing(scope); e != nil {
				return e
			}
		}
		definitions, plugins, e := m.nativeCommandDefinitions(ctx, scope)
		if e != nil {
			return e
		}
		m.Registry.ReplaceNativePrompts(definitions, plugins, false)
		if m.options.OnCommandsChanged != nil {
			return m.options.OnCommandsChanged(m.Registry.ControlCommands())
		}
		return nil
	})
	if e != nil {
		return e
	}
	select {
	case <-receipt.Done():
		return receipt.Wait()
	case <-ctx.Done():
		return ctx.Err()
	}
}
