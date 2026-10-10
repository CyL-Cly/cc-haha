package mcpapi

import "sync"

// EnableQueue retains the original per-project/server probe ordering. Disable
// persistence and session synchronization never wait behind this queue.
type EnableQueue struct {
	mu    sync.Mutex
	tails map[string]chan struct{}
}

func (q *EnableQueue) Enter(key string) func() {
	q.mu.Lock()
	if q.tails == nil {
		q.tails = map[string]chan struct{}{}
	}
	previous := q.tails[key]
	current := make(chan struct{})
	q.tails[key] = current
	q.mu.Unlock()
	if previous != nil {
		<-previous
	}
	return func() {
		q.mu.Lock()
		if q.tails[key] == current {
			delete(q.tails, key)
		}
		close(current)
		q.mu.Unlock()
	}
}
