package sessionstore

import (
	"context"
	"sync"
)

type Lane string

const (
	PageLane     Lane = "page"
	ContextLane  Lane = "context"
	RecoveryLane Lane = "recovery"
	MetadataLane Lane = "metadata"
)

type waiter struct {
	ready   chan struct{}
	started bool
}
type pool struct {
	active, capacity, queueCapacity int
	waiting                         []*waiter
}
type Budget struct {
	mu    sync.Mutex
	pools map[Lane]*pool
}

func NewBudget() *Budget {
	return &Budget{pools: map[Lane]*pool{PageLane: {capacity: 2, queueCapacity: 8}, ContextLane: {capacity: 1, queueCapacity: 4}, RecoveryLane: {capacity: 1, queueCapacity: 2}, MetadataLane: {capacity: 2, queueCapacity: 128}}}
}
func (budget *Budget) acquire(ctx context.Context, lane Lane) (func(), error) {
	if err := check(ctx); err != nil {
		return nil, err
	}
	budget.mu.Lock()
	p := budget.pools[lane]
	if p == nil {
		budget.mu.Unlock()
		panic("unknown history admission lane")
	}
	if p.active >= p.capacity {
		if len(p.waiting) >= p.queueCapacity {
			budget.mu.Unlock()
			return nil, &APIError{429, "HISTORY_BUSY", "History reader is busy; retry shortly"}
		}
		item := &waiter{ready: make(chan struct{})}
		p.waiting = append(p.waiting, item)
		budget.mu.Unlock()
		select {
		case <-item.ready:
		case <-ctx.Done():
			budget.mu.Lock()
			if !item.started {
				for index, queued := range p.waiting {
					if queued == item {
						p.waiting = append(p.waiting[:index], p.waiting[index+1:]...)
						break
					}
				}
				budget.mu.Unlock()
				return nil, check(ctx)
			}
			budget.mu.Unlock()
		}
	} else {
		p.active++
		budget.mu.Unlock()
	}
	return func() {
		budget.mu.Lock()
		defer budget.mu.Unlock()
		if len(p.waiting) > 0 {
			next := p.waiting[0]
			p.waiting = p.waiting[1:]
			next.started = true
			close(next.ready)
		} else {
			p.active--
		}
	}, nil
}
func (budget *Budget) With(ctx context.Context, lane Lane, read func() error) error {
	release, err := budget.acquire(ctx, lane)
	if err != nil {
		return err
	}
	defer release()
	if err = check(ctx); err != nil {
		return err
	}
	return read()
}
