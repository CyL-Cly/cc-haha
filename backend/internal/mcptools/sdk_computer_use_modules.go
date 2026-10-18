package mcptools

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativemodule"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"time"
)

type computerUseModuleKey uint8

const (
	computerUseCleanupModule computerUseModuleKey = iota
	computerUseDaemonModule
	computerUseBadgeModule
	computerUseModuleCount
)

// Only the three actual CU namespaces live in this private cache. The shared
// prelinked loader owns their import protocol, not their export authority.
type computerUseModuleEntry struct {
	record *computerUseModuleRecord
	module *nativemodule.Entry[computerUseModuleKey, *computerUseModuleNamespace]
}

type computerUseModuleRecord struct {
	service      *Service
	key          computerUseModuleKey
	linked       bool
	evaluated    bool
	namespace    *computerUseModuleNamespace
	cleanupState *computerUseCleanupModuleState
	overlayState *inactiveComputerUseCleanup
}

type computerUseCleanupModuleState struct{ overlayHideTimeout time.Duration }

type computerUseModuleNamespace struct {
	record        *computerUseModuleRecord
	cleanupExport func(*cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error)
	overlayExport func(*cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error)
	badgeExport   func(*cooperative.Scope) error
}

func (s *Service) computerUseModuleEntry(scope *cooperative.Scope, key computerUseModuleKey) (*computerUseModuleEntry, error) {
	if s == nil || !scope.BelongsTo(s.options.Owner) || key >= computerUseModuleCount {
		return nil, cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return nil, err
	}
	if entry := s.inactiveComputerUse.modules[key]; entry != nil {
		if entry.module == nil || entry.record == nil {
			return nil, cooperative.ErrRestricted
		}
		return entry, nil
	}
	record := &computerUseModuleRecord{service: s, key: key}
	record.namespace = &computerUseModuleNamespace{record: record}
	entry := &computerUseModuleEntry{record: record}
	// Publish the single pending declaration before admitting initialization.
	s.inactiveComputerUse.modules[key] = entry
	module, err := nativemodule.NewOwnerInitialized(s.options.Owner, scope, key, record.namespace, func(next *cooperative.Scope, actualKey computerUseModuleKey, namespace *computerUseModuleNamespace) error {
		if actualKey != key || namespace != record.namespace {
			return cooperative.ErrRestricted
		}
		return record.admitted(next)
	}, record.linkExports, record.evaluateState)
	if err != nil {
		// No initialization was admitted. This importer must not poison a later
		// valid importer with its own cancelled/failed admission placeholder.
		if s.inactiveComputerUse.modules[key] == entry {
			s.inactiveComputerUse.modules[key] = nil
		}
		return nil, err
	}
	entry.module = module
	return entry, nil
}

func (record *computerUseModuleRecord) admitted(scope *cooperative.Scope) error {
	if record == nil || record.service == nil || !scope.BelongsTo(record.service.options.Owner) || record.key >= computerUseModuleCount || record.namespace == nil || record.namespace.record != record {
		return cooperative.ErrRestricted
	}
	entry := record.service.inactiveComputerUse.modules[record.key]
	if entry == nil || entry.record != record {
		return cooperative.ErrRestricted
	}
	return scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
}

func (record *computerUseModuleRecord) linkExports(scope *cooperative.Scope) error {
	if err := record.admitted(scope); err != nil {
		return err
	}
	if record.linked || record.evaluated {
		return cooperative.ErrRestricted
	}
	namespace, service := record.namespace, record.service
	switch record.key {
	case computerUseCleanupModule:
		namespace.cleanupExport = func(next *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
			if err := record.check(next); err != nil {
				return nil, err
			}
			if record.cleanupState == nil {
				return nil, cooperative.ErrRestricted
			}
			return service.inactiveComputerUseAfterTurnWithTimeout(next, record.cleanupState.overlayHideTimeout)
		}
	case computerUseDaemonModule:
		namespace.overlayExport = func(next *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
			if err := record.check(next); err != nil {
				return nil, err
			}
			if record.overlayState != &service.inactiveComputerUse {
				return nil, cooperative.ErrRestricted
			}
			return service.hideInactiveComputerUseOverlay(next)
		}
	case computerUseBadgeModule:
		// No CU binder means no owned badge process. Keep the actual inactive
		// export bound to this namespace/Owner; never start a helper to hide it.
		namespace.badgeExport = func(next *cooperative.Scope) error { return record.check(next) }
	default:
		return cooperative.ErrRestricted
	}
	record.linked = true
	return nil
}

func (record *computerUseModuleRecord) evaluateState(scope *cooperative.Scope) error {
	if err := record.admitted(scope); err != nil {
		return err
	}
	if !record.linked || record.evaluated {
		return cooperative.ErrRestricted
	}
	switch record.key {
	case computerUseCleanupModule:
		// Source cleanup.ts's real module constant is consumed by its race.
		record.cleanupState = &computerUseCleanupModuleState{overlayHideTimeout: 2 * time.Second}
	case computerUseDaemonModule:
		// Bind the existing canonical overlay state. Do not reset a live
		// reconciliation or manufacture a daemon/lock/native operation.
		record.overlayState = &record.service.inactiveComputerUse
	case computerUseBadgeModule:
		if record.namespace.badgeExport == nil {
			return cooperative.ErrRestricted
		}
	default:
		return cooperative.ErrRestricted
	}
	record.evaluated = true
	return nil
}

func (record *computerUseModuleRecord) check(scope *cooperative.Scope) error {
	if err := record.admitted(scope); err != nil {
		return err
	}
	if !record.linked || !record.evaluated {
		return cooperative.ErrRestricted
	}
	return nil
}

func (s *Service) importComputerUseModule(scope *cooperative.Scope, key computerUseModuleKey) (*ownerasync.OwnerFuture[*computerUseModuleNamespace], error) {
	entry, err := s.computerUseModuleEntry(scope, key)
	if err != nil {
		return nil, err
	}
	return entry.module.Import(scope)
}

func (namespace *computerUseModuleNamespace) cleanup(scope *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
	if namespace == nil || namespace.record == nil || namespace.record.key != computerUseCleanupModule {
		return nil, cooperative.ErrRestricted
	}
	if err := namespace.record.check(scope); err != nil {
		return nil, err
	}
	if namespace.cleanupExport == nil {
		return nil, cooperative.ErrRestricted
	}
	return namespace.cleanupExport(scope)
}

func (namespace *computerUseModuleNamespace) overlayHide(scope *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
	if namespace == nil || namespace.record == nil || namespace.record.key != computerUseDaemonModule {
		return nil, cooperative.ErrRestricted
	}
	if err := namespace.record.check(scope); err != nil {
		return nil, err
	}
	if namespace.overlayExport == nil {
		return nil, cooperative.ErrRestricted
	}
	return namespace.overlayExport(scope)
}

func (namespace *computerUseModuleNamespace) hideCursorBadge(scope *cooperative.Scope) error {
	if namespace == nil || namespace.record == nil || namespace.record.key != computerUseBadgeModule {
		return cooperative.ErrRestricted
	}
	// Without a CU binder there is no running badge. The actual hide callback
	// exits at this local pre-check and cannot spawn a process or touch disk.
	if namespace.badgeExport == nil {
		return cooperative.ErrRestricted
	}
	return namespace.badgeExport(scope)
}
