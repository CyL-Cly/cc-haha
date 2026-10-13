package clibootstrap

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// Desktop installation and its canonical Go runtimes share one process. A
// publication invalidates the same private config's live command snapshots;
// standalone processes receive the physical file detector instead.
var nativeSkillChanges = struct {
	sync.Mutex
	next     uint64
	byConfig map[string]map[uint64]func() error
}{byConfig: map[string]map[uint64]func() error{}}

func nativeSkillConfigKey(directory string) string {
	directory = filepath.Clean(directory)
	// A first-install directory may not exist yet. Canonicalize its existing
	// ancestor as well, so creating it does not change /var vs /private/var
	// identity between subscription and publication.
	current := directory
	var missing []string
	for {
		if actual, err := filepath.EvalSymlinks(current); err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				actual = filepath.Join(actual, missing[i])
			}
			return filepath.Clean(actual)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
	return directory
}

func subscribeNativeSkillChanges(directory string, notify func() error) func() {
	key := nativeSkillConfigKey(directory)
	nativeSkillChanges.Lock()
	nativeSkillChanges.next++
	id := nativeSkillChanges.next
	if nativeSkillChanges.byConfig[key] == nil {
		nativeSkillChanges.byConfig[key] = map[uint64]func() error{}
	}
	nativeSkillChanges.byConfig[key][id] = notify
	nativeSkillChanges.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			nativeSkillChanges.Lock()
			defer nativeSkillChanges.Unlock()
			delete(nativeSkillChanges.byConfig[key], id)
			if len(nativeSkillChanges.byConfig[key]) == 0 {
				delete(nativeSkillChanges.byConfig, key)
			}
		})
	}
}

// NotifyNativeSkillCommandsChanged enqueues each matching owner before the
// installation HTTP acknowledgement. It neither starts an ASK nor reloads MCP.
func NotifyNativeSkillCommandsChanged(directory string) error {
	key := nativeSkillConfigKey(directory)
	nativeSkillChanges.Lock()
	listeners := make([]func() error, 0, len(nativeSkillChanges.byConfig[key]))
	for _, notify := range nativeSkillChanges.byConfig[key] {
		listeners = append(listeners, notify)
	}
	nativeSkillChanges.Unlock()
	var failed []error
	for _, notify := range listeners {
		failed = append(failed, notify())
	}
	return errors.Join(failed...)
}

func (m *MainPrint) refreshManagedSkillCommandsOnOwner(ctx context.Context, scope *cooperative.Scope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if m.closed || m.shuttingDown {
		return cooperative.ErrClosed
	}
	// Match the market producer's clearSkillCaches and the headless command
	// subscriber, preserving dynamically loaded definitions and current history.
	m.skillCommands.Clear()
	if err := m.workflowDefinitions.ClearCommandsOnOwner(scope); err != nil {
		return err
	}
	if err := m.resetNativeSkillListing(scope); err != nil {
		return err
	}
	definitions, plugins, err := m.nativeCommandDefinitions(ctx, scope)
	if err != nil {
		return err
	}
	m.Registry.ReplaceNativePrompts(definitions, plugins, false)
	if m.options.OnCommandsChanged != nil {
		return m.options.OnCommandsChanged(m.Registry.ControlCommands())
	}
	return nil
}

func (s *nativeSkillWatcher) enqueueManagedRefresh(m *MainPrint) error {
	s.managedMu.Lock()
	defer s.managedMu.Unlock()
	if s.managedClosed {
		return nil
	}
	receipt, err := m.Owner.Submit(s.managedContext, cooperative.Input, func(scope *cooperative.Scope) error {
		return m.refreshManagedSkillCommandsOnOwner(s.managedContext, scope)
	})
	if err != nil {
		return err
	}
	s.managedPending.Add(1)
	go func() {
		defer s.managedPending.Done()
		select {
		case <-receipt.Done():
			if err := receipt.Wait(); err != nil && s.managedContext.Err() == nil {
				m.reportNativeSkillRefreshError(err)
			}
		case <-s.managedContext.Done():
		}
	}()
	return nil
}
