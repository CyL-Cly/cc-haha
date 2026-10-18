package mcptools

import (
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// These are the cuHelperDaemon module's never-started overlay fields. They
// still change on a non-CU turn: overlayHide requests cleanup, increments the
// revision, and owns a reconcile Promise even when its loop has no work.
type inactiveComputerUseCleanup struct {
	modules                 [computerUseModuleCount]*computerUseModuleEntry
	overlayCleanupRequested bool
	overlayRevision         uint64
	overlayReconcilePromise *ownerasync.OwnerFuture[struct{}]
}

func (s *Service) fulfilledComputerUseCleanupPromise(scope *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
	loaded, err := ownerasync.NewOwnerFuture[struct{}](s.options.Owner)
	if err == nil {
		err = loaded.Resolve(scope, struct{}{})
	}
	return loaded, err
}

// Both original CU finally callbacks return undefined. Frozen Bun's native
// finally retains the parent's result in that callback's reaction; allocating
// and adopting an extra Promise here changes the actual cleanup reaction graph.
func (s *Service) computerUseFinally(scope *cooperative.Scope, parent *ownerasync.OwnerFuture[struct{}], run func(*cooperative.Scope) error) (*ownerasync.OwnerFuture[struct{}], error) {
	if s == nil || !scope.BelongsTo(s.options.Owner) || run == nil {
		return nil, cooperative.ErrRestricted
	}
	return parent.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if err := run(next); err != nil {
			return err
		}
		return failure
	})
}

func (s *Service) hideInactiveComputerUseOverlay(scope *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
	state := &s.inactiveComputerUse
	state.overlayCleanupRequested = true
	state.overlayRevision++
	if state.overlayReconcilePromise == nil {
		// The original async reconcileOverlay has zero loop iterations while
		// neither an overlay nor an active daemon turn exists. It never starts
		// a daemon, probes a lock, or performs any other syscall on this path.
		reconcile, err := s.fulfilledComputerUseCleanupPromise(scope)
		if err != nil {
			return nil, err
		}
		state.overlayReconcilePromise, err = s.computerUseFinally(scope, reconcile, func(*cooperative.Scope) error {
			state.overlayReconcilePromise = nil
			// needsOverlayReconciliation remains false: no active turn and no
			// actual overlay. cleanupRequested intentionally remains true.
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return state.overlayReconcilePromise, nil
}

func (s *Service) inactiveComputerUseAfterTurn(scope *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
	return s.inactiveComputerUseAfterTurnWithTimeout(scope, 2*time.Second)
}

func (s *Service) inactiveComputerUseAfterTurnWithTimeout(scope *cooperative.Scope, overlayHideTimeout time.Duration) (*ownerasync.OwnerFuture[struct{}], error) {
	// The default badge callback is fire-and-forget import().then(hide).catch.
	// No binder means there cannot be a running badge or hidden applications.
	badgeModule, err := s.importComputerUseModule(scope, computerUseBadgeModule)
	if err != nil {
		return nil, err
	}
	badge, err := badgeModule.Then(scope, func(next *cooperative.Scope, namespace *computerUseModuleNamespace, failure error) error {
		if failure != nil {
			return failure
		}
		return namespace.hideCursorBadge(next)
	})
	if err != nil {
		return nil, err
	}
	if _, err = badge.Then(scope, func(*cooperative.Scope, struct{}, error) error { return nil }); err != nil {
		return nil, err
	}

	race, err := ownerasync.NewOwnerFuture[struct{}](s.options.Owner)
	if err != nil {
		return nil, err
	}
	// The timeout's resolver targets the race directly. Clearing this timer
	// leaves no unobservable pending OwnerFuture/worker for the losing arm.
	timer, err := scope.ArmTimer(overlayHideTimeout, func() {
		// The timeout resolver settles its Promise; Promise.race forwards on
		// the next reaction, using that reaction's current owner scope.
		_, _ = s.options.Owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error { return race.Resolve(next, struct{}{}) })
	})
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = scope.ClearTimer(timer)
		}
	}()
	overlay, err := ownerasync.NewOwnerFuture[struct{}](s.options.Owner)
	if err != nil {
		return nil, err
	}
	daemonModule, err := s.importComputerUseModule(scope, computerUseDaemonModule)
	if err != nil {
		return nil, err
	}
	_, err = daemonModule.Then(scope, func(next *cooperative.Scope, namespace *computerUseModuleNamespace, failure error) error {
		if failure != nil {
			return overlay.Reject(next, failure)
		}
		reconcile, err := namespace.overlayHide(next)
		if err != nil {
			return overlay.Reject(next, err)
		}
		_, err = reconcile.Then(next, func(next *cooperative.Scope, _ struct{}, failure error) error {
			if failure != nil {
				return overlay.Reject(next, failure)
			}
			return overlay.Resolve(next, struct{}{})
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	caught, err := overlay.Then(scope, func(*cooperative.Scope, struct{}, error) error { return nil })
	if err != nil {
		return nil, err
	}
	if _, err = caught.Then(scope, func(next *cooperative.Scope, _ struct{}, _ error) error { return race.Resolve(next, struct{}{}) }); err != nil {
		return nil, err
	}
	finished, err := s.computerUseFinally(scope, race, func(next *cooperative.Scope) error { return next.ClearTimer(timer) })
	if err != nil {
		return nil, err
	}
	// cleanupComputerUseAfterTurn resumes here, reads its empty CU app state,
	// and returns at the zero-syscall isLockHeldLocally pre-check.
	result, err := finished.Then(scope, func(_ *cooperative.Scope, _ struct{}, failure error) error { return failure })
	success = err == nil
	return result, err
}

func (s *Service) beginInactiveComputerUseTurn(scope *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
	// The original stop/abort caller awaits its actual import before invoking
	// cleanup. The first actual module initialization settles its load/state;
	// every import retains the original loader graph, join, and namespace.
	module, err := s.importComputerUseModule(scope, computerUseCleanupModule)
	if err != nil {
		return nil, err
	}
	completed, err := ownerasync.NewOwnerFuture[struct{}](s.options.Owner)
	if err != nil {
		return nil, err
	}
	_, err = module.Then(scope, func(next *cooperative.Scope, namespace *computerUseModuleNamespace, failure error) error {
		if failure != nil {
			return completed.Reject(next, failure)
		}
		cleanup, err := namespace.cleanup(next)
		if err != nil {
			return completed.Reject(next, err)
		}
		_, err = cleanup.Then(next, func(next *cooperative.Scope, _ struct{}, failure error) error {
			if failure != nil {
				return completed.Reject(next, failure)
			}
			return completed.Resolve(next, struct{}{})
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return completed, nil
}

func (s *Service) endInactiveComputerUseTurn(scope *cooperative.Scope) error {
	completed, err := s.beginInactiveComputerUseTurn(scope)
	if err != nil {
		return err
	}
	_, err = completed.Await(scope)
	return err
}
