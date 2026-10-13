package localindex

import (
	"context"
	"sync"
)

type projectionWaiter struct {
	ready   chan struct{}
	granted bool
}
type projectionBudget struct {
	mu      sync.Mutex
	active  bool
	waiting []*projectionWaiter
}

// MAIN admits one projection (including direct callers) and eight waiters.
// The native runtime uses a shared gate rather than spawning a JS worker.
var searchProjectionBudget projectionBudget

func (b *projectionBudget) acquire(ctx context.Context) (func(), error) {
	if e := context.Cause(ctx); e != nil {
		return nil, e
	}
	b.mu.Lock()
	if b.active {
		if len(b.waiting) >= 8 {
			b.mu.Unlock()
			return nil, coded("SEARCH_CONTENT_BUSY", "SEARCH_CONTENT_BUSY")
		}
		w := &projectionWaiter{ready: make(chan struct{})}
		b.waiting = append(b.waiting, w)
		b.mu.Unlock()
		select {
		case <-w.ready:
		case <-ctx.Done():
			b.mu.Lock()
			if !w.granted {
				for i, queued := range b.waiting {
					if queued == w {
						b.waiting = append(b.waiting[:i], b.waiting[i+1:]...)
						break
					}
				}
				b.mu.Unlock()
				return nil, context.Cause(ctx)
			}
			b.mu.Unlock()
		}
	} else {
		b.active = true
		b.mu.Unlock()
	}
	release := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if len(b.waiting) > 0 {
			next := b.waiting[0]
			b.waiting = b.waiting[1:]
			next.granted = true
			close(next.ready)
		} else {
			b.active = false
		}
	}
	if e := context.Cause(ctx); e != nil {
		release()
		return nil, e
	}
	return release, nil
}

type cancellableMutex struct {
	once sync.Once
	gate chan struct{}
}

func (m *cancellableMutex) lock(ctx context.Context) error {
	m.once.Do(func() { m.gate = make(chan struct{}, 1) })
	select {
	case m.gate <- struct{}{}:
		if e := context.Cause(ctx); e != nil {
			m.unlock()
			return e
		}
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
func (m *cancellableMutex) unlock() { <-m.gate }
