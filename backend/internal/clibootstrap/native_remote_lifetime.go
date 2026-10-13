package clibootstrap

import (
	"context"
	"sync"
)

// Original v1 persistence failure invokes process shutdown, even when its
// independent record promise was merely diagnostic. The Go process has one
// shared model/input runtime to cancel; ordinary ASK interrupts don't own this
// lifetime. Native jobs are joined by the existing transcript cleanup boundary.
type nativeRemoteLifetime struct {
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	closed    bool
	failure   error
	runCancel context.CancelCauseFunc
}

func (life *nativeRemoteLifetime) context() context.Context {
	life.mu.Lock()
	defer life.mu.Unlock()
	if life.ctx == nil {
		life.ctx, life.cancel = context.WithCancel(context.Background())
		if life.closed || life.failure != nil {
			life.cancel()
		}
	}
	return life.ctx
}
func (life *nativeRemoteLifetime) close() {
	life.mu.Lock()
	defer life.mu.Unlock()
	life.closed = true
	if life.cancel != nil {
		life.cancel()
	}
}
func (life *nativeRemoteLifetime) bindRun(cancel context.CancelCauseFunc) {
	life.mu.Lock()
	defer life.mu.Unlock()
	life.runCancel = cancel
	if cancel != nil && life.failure != nil {
		cancel(life.failure)
	}
}
func (life *nativeRemoteLifetime) fail(err error) {
	life.mu.Lock()
	defer life.mu.Unlock()
	if life.failure != nil {
		return
	}
	life.failure = err
	if life.cancel != nil {
		life.cancel()
	}
	if life.runCancel != nil {
		life.runCancel(err)
	}
}
func (life *nativeRemoteLifetime) err() error {
	life.mu.Lock()
	defer life.mu.Unlock()
	return life.failure
}
