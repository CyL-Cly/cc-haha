package mcptools

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

func TestComputerUseModuleImportsKeepCacheNamespaceAndPerCallPromiseIdentities(t *testing.T) {
	s, refs := inactiveComputerUseService(t)
	if err := s.options.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for key := computerUseCleanupModule; key < computerUseModuleCount; key++ {
			one, err := s.importComputerUseModule(scope, key)
			if err != nil {
				return err
			}
			two, err := s.importComputerUseModule(scope, key)
			if err != nil {
				return err
			}
			if one == two {
				t.Error("each import requires its own user Promise")
			}
			first, err := one.Await(scope)
			if err != nil {
				return err
			}
			second, err := two.Await(scope)
			if err != nil {
				return err
			}
			entry := s.inactiveComputerUse.modules[key]
			if first != second || first != entry.record.namespace || first.record.key != key {
				t.Error("import copied or replaced the cached namespace")
			}
			if key != computerUseCleanupModule {
				if _, err := first.cleanup(scope); !errors.Is(err, cooperative.ErrRestricted) {
					t.Error("foreign cleanup export admitted", err)
				}
			}
			if key != computerUseDaemonModule {
				if _, err := first.overlayHide(scope); !errors.Is(err, cooperative.ErrRestricted) {
					t.Error("foreign overlay export admitted", err)
				}
			}
			if key != computerUseBadgeModule {
				if err := first.hideCursorBadge(scope); !errors.Is(err, cooperative.ErrRestricted) {
					t.Error("foreign badge export admitted", err)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count, _ := refs.Snapshot(); count != 0 {
		t.Fatal("prelinked import allocated physical work", count)
	}
}

// The original private two-arm/evaluation-capability tests now execute the
// same reused implementation in nativemodule/prelinked_test.go. This consumer
// test reaches its failure cache through the actual CU namespace validator.
func TestComputerUseModuleImportsPreserveCachedFailureAfterActualLinkRejection(t *testing.T) {
	s, refs := inactiveComputerUseService(t)
	if err := s.options.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		entry, err := s.computerUseModuleEntry(scope, computerUseCleanupModule)
		if err != nil {
			return err
		}
		original := entry.record.namespace
		pending, err := s.importComputerUseModule(scope, computerUseCleanupModule)
		if err != nil {
			return err
		}
		select {
		case <-pending.Done():
			t.Error("public import completed before queued graph consumption")
		default:
		}
		// Import has already passed its admission validator. Its real queued
		// graph consumer must reject a changed record/namespace link.
		entry.record.namespace = &computerUseModuleNamespace{record: entry.record}
		if _, err = pending.Await(scope); err != cooperative.ErrRestricted {
			t.Error("actual namespace link rejection changed", err)
		}
		entry.record.namespace = original
		for range 2 {
			next, err := s.importComputerUseModule(scope, computerUseCleanupModule)
			if err != nil {
				return err
			}
			if _, err = next.Await(scope); err != cooperative.ErrRestricted {
				t.Error("restored link replaced original cached failure", err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count, _ := refs.Snapshot(); count != 0 {
		t.Fatal("module guard created native I/O", count)
	}
}

func TestComputerUseModuleImportRejectionCancellationCloseAndScopeGuards(t *testing.T) {
	s, _ := inactiveComputerUseService(t)
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := s.importComputerUseModule(scope, computerUseCleanupModule); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign module import admitted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var expired *cooperative.Scope
	if err := s.options.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		expired = scope
		if _, err := s.importComputerUseModule(scope, computerUseModuleCount); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("unknown module admitted", err)
		}
		if _, err := s.importComputerUseModule(nil, computerUseCleanupModule); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("nil module scope admitted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.importComputerUseModule(expired, computerUseCleanupModule); !errors.Is(err, cooperative.ErrRestricted) {
		t.Error("retained module scope admitted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var result *ownerasync.OwnerFuture[*computerUseModuleNamespace]
	if err := s.options.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		result, err = s.importComputerUseModule(scope, computerUseCleanupModule)
		if err != nil {
			return err
		}
		select {
		case <-result.Done():
			t.Error("actual public import was not pending before cancellation")
		default:
		}
		cancel()
		if _, err = result.Await(scope); !errors.Is(err, context.Canceled) {
			t.Error("cancelled import wait remained active", err)
		}
		return nil
	}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Caller cancellation ends its wait; the admitted module still has its own
	// immutable result. Low-level held evaluation/global-close is preserved in
	// the shared package, rather than adding a mutable production test setter.
	namespace, err := result.Wait(context.Background())
	if err != nil || namespace != s.inactiveComputerUse.modules[computerUseCleanupModule].record.namespace {
		t.Fatal("cancellation changed the admitted namespace result", namespace, err)
	}
	s.options.Owner.Close()
	if _, err := s.importComputerUseModule(expired, computerUseCleanupModule); !errors.Is(err, cooperative.ErrRestricted) {
		t.Error("closed owner accepted retained module export", err)
	}
}

func TestComputerUseModulePublicImportCloseBeforeQueuedGraphCannotCallLateExport(t *testing.T) {
	s, refs := inactiveComputerUseService(t)
	startClose, closeDone := make(chan struct{}), make(chan struct{})
	var closeOnce sync.Once
	requestClose := func() { closeOnce.Do(func() { close(startClose) }) }
	t.Cleanup(requestClose)
	go func() { <-startClose; s.options.Owner.Close(); close(closeDone) }()
	var result *ownerasync.OwnerFuture[*computerUseModuleNamespace]
	called := false
	err := s.options.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var err error
		result, err = s.importComputerUseModule(scope, computerUseCleanupModule)
		if err != nil {
			return err
		}
		if _, err = result.Then(scope, func(next *cooperative.Scope, namespace *computerUseModuleNamespace, failure error) error {
			if failure != nil {
				return failure
			}
			called = true
			_, err := namespace.cleanup(next)
			return err
		}); err != nil {
			return err
		}
		requestClose()
		// Keep this original stack active until actual off-owner Close cancels
		// its context. No queued graph callback runs during this test handshake.
		for {
			err = scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
			if err != nil {
				return err
			}
			runtime.Gosched()
		}
	})
	if !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("actual close did not terminate active import owner", err)
	}
	<-closeDone
	if _, err := result.Wait(context.Background()); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("closed owner left public import pending or successful", err)
	}
	if called {
		t.Fatal("late module completion called a CU export after close")
	}
	if count, _ := refs.Snapshot(); count != 0 {
		t.Fatal("module close created physical work", count)
	}
}
