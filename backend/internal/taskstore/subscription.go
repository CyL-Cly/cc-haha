package taskstore

import "sync"

// The existing tasksUpdated producer fires after each successful physical
// mutation. Subscribers receive a wake only; they read the same durable list.
type updateSubscriptions struct {
	mu       sync.Mutex
	sequence uint64
	values   map[uint64]func()
}

func (s *Service) SubscribeUpdates(changed func()) func() {
	if changed == nil {
		return func() {}
	}
	s.updates.mu.Lock()
	if s.updates.values == nil {
		s.updates.values = map[uint64]func(){}
	}
	s.updates.sequence++
	id := s.updates.sequence
	s.updates.values[id] = changed
	s.updates.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { s.updates.mu.Lock(); delete(s.updates.values, id); s.updates.mu.Unlock() }) }
}

func (s *Service) publishUpdates() {
	s.updates.mu.Lock()
	listeners := make([]func(), 0, len(s.updates.values))
	for _, callback := range s.updates.values {
		listeners = append(listeners, callback)
	}
	s.updates.mu.Unlock()
	for _, callback := range listeners {
		// Source tasksUpdated catches listener errors. These read-only wakes
		// cannot turn a committed task mutation into a failed tool call.
		func() { defer func() { _ = recover() }(); callback() }()
	}
}
