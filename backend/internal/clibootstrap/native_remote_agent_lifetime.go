package clibootstrap

import (
	"context"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// This lifetime owns detached native task consumers, not a second task store.
// AppState tasks and every poll/status transition remain on the existing Owner.
type nativeRemoteAgentLifetime struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	closed  bool
	workers sync.WaitGroup
	// The output map is accessed only on Owner, just like Source's disk-output
	// module cache. mu protects administrative lifetime state only.
	outputs map[string]*nativeRemoteAgentOutput
	mounted bool
}

func (life *nativeRemoteAgentLifetime) context() context.Context {
	life.mu.Lock()
	defer life.mu.Unlock()
	if life.ctx == nil {
		life.ctx, life.cancel = context.WithCancel(context.Background())
		if life.closed {
			life.cancel()
		}
	}
	return life.ctx
}

func (life *nativeRemoteAgentLifetime) launch(m *MainPrint, work func(context.Context, *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error)) (<-chan struct{}, error) {
	life.mu.Lock()
	if life.closed {
		life.mu.Unlock()
		return nil, cooperative.ErrClosed
	}
	if life.ctx == nil {
		life.ctx, life.cancel = context.WithCancel(context.Background())
	}
	ctx := life.ctx
	life.workers.Add(1)
	life.mu.Unlock()
	done := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { life.workers.Done(); close(done) }) }
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		pending, err := work(ctx, scope)
		if err != nil {
			finish()
			return err
		}
		_, err = pending.Then(scope, func(*cooperative.Scope, struct{}, error) error { finish(); return nil })
		if err != nil {
			finish()
		}
		return err
	})
	if err != nil {
		finish()
		return done, err
	}
	// Only administrative receipt failure is handled off Owner. The real
	// producer returns immediately; its native completions use Promise reactions.
	go func() {
		if receipt.Wait() != nil {
			finish()
		}
	}()
	return done, nil
}

func (life *nativeRemoteAgentLifetime) close() {
	life.mu.Lock()
	defer life.mu.Unlock()
	life.closed = true
	if life.cancel != nil {
		life.cancel()
	}
}

func (life *nativeRemoteAgentLifetime) join(ctx context.Context) error {
	done := make(chan struct{})
	go func() { life.workers.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
