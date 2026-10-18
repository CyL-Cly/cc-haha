package clibootstrap

import (
	"context"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

// The two actual macOS subprocesses belong to this concrete command factory.
// Their callbacks and Promise.all publication run on the same canonical Owner;
// Close joins physical references outside Owner before closing that Owner.
type nativeCommandKeychainPrefetch struct {
	command   *MainCommand
	owner     *cooperative.Owner
	native    *NativeBoundary
	store     *securestorage.Store
	ticket    *securestorage.KeychainPrefetch
	ctx       context.Context
	cancel    context.CancelFunc
	refs      ownerasync.NativeReferences
	completed *ownerasync.OwnerFuture[struct{}]
	arms      []<-chan struct{}
	settled   int // Owner only
	mu        sync.Mutex
	closed    bool
}

func (runtime *nativeCommandKeychainPrefetch) check(scope *cooperative.Scope) error {
	if runtime == nil || scope == nil || !scope.BelongsTo(runtime.owner) || runtime.command == nil {
		return cooperative.ErrRestricted
	}
	m := runtime.command
	if runtime.owner != m.Owner || runtime.native != m.Native || runtime.store != m.Secure || !runtime.ticket.Owns(m.Secure) || !m.Native.NativeProcess {
		return cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return err
	}
	runtime.mu.Lock()
	closed := runtime.closed
	runtime.mu.Unlock()
	if closed {
		return cooperative.ErrClosed
	}
	return context.Cause(runtime.ctx)
}

func (m *MainCommand) startNativeCommandKeychainPrefetch(ctx context.Context, scope *cooperative.Scope) error {
	if m == nil || ctx == nil || scope == nil || !scope.BelongsTo(m.Owner) || m.Native == nil || !m.Native.NativeProcess || m.Secure == nil {
		return cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return err
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if m.credentialPlatform != "darwin" || envTruthyNative(m.Native.Environment["CLAUDE_CODE_SIMPLE"]) {
		return nil
	}
	if m.nativeKeychainPrefetch != nil {
		return m.nativeKeychainPrefetch.check(scope) // same process starts once
	}
	life, cancel := context.WithCancel(ctx)
	ticket, err := m.Secure.NewKeychainPrefetch(life)
	if err != nil {
		cancel()
		return err
	}
	runtime := &nativeCommandKeychainPrefetch{command: m, owner: m.Owner, native: m.Native, store: m.Secure, ticket: ticket, ctx: life, cancel: cancel}
	m.nativeKeychainPrefetch = runtime // retain even if later dispatch/setup fails
	binding, err := runtime.refs.Bind(m.Owner)
	if err != nil {
		return err
	}
	joined, err := ownerasync.NewOwnerFuture[struct{}](m.Owner)
	if err != nil {
		return err
	}
	// The cached prefetchPromise is Promise.all(...).then(prime), not the
	// final arm's own reaction. Keep that actual publication continuation.
	runtime.completed, err = joined.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			return failure
		}
		if err := runtime.check(next); err != nil {
			return err
		}
		return ticket.Publish()
	})
	if err != nil {
		return err
	}
	// execFile synchronously admits both actual subprocesses. Merely placing
	// two blocking RunSecurity calls on goroutines cannot provide this phase.
	if err := ticket.StartOAuth(); err != nil {
		return err
	}
	if err := ticket.StartLegacy(); err != nil {
		return err
	}
	for _, arm := range []func() error{ticket.RunOAuth, ticket.RunLegacy} {
		done := make(chan struct{})
		work, err := nativeOwnedWork(m.Owner, binding, scope, func() (struct{}, error) {
			defer close(done)
			return struct{}{}, arm()
		})
		if err != nil {
			// nativeOwnedWork fails only before starting its goroutine; this
			// channel was never transferred to a Wait worker or to runtime.arms.
			// Ticket.Close still reaps any process admitted above.
			return err
		}
		runtime.arms = append(runtime.arms, done)
		_, err = work.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
			if failure == nil {
				failure = runtime.check(next)
			}
			if failure != nil {
				return joined.Reject(next, failure)
			}
			runtime.settled++
			if runtime.settled != 2 {
				return nil
			}
			return joined.Resolve(next, struct{}{})
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *MainCommand) awaitNativeCommandKeychainPrefetch(scope *cooperative.Scope) error {
	if m == nil || scope == nil || !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	runtime := m.nativeKeychainPrefetch
	if runtime == nil {
		return nil
	}
	if err := runtime.check(scope); err != nil {
		return err
	}
	_, err := runtime.completed.Await(scope)
	return err
}

func (m *MainCommand) closeNativeCommandKeychainPrefetch() {
	if m == nil || m.nativeKeychainPrefetch == nil {
		return
	}
	runtime := m.nativeKeychainPrefetch
	runtime.mu.Lock()
	runtime.closed = true
	runtime.mu.Unlock()
	runtime.cancel()
	runtime.ticket.Close()
	for _, done := range runtime.arms {
		<-done
	}
	if count, idle := runtime.refs.Snapshot(); count != 0 {
		<-idle
	}
}
