package workflow

import (
	"context"
	"sync"
)

type runtimeAdmission struct {
	actor any
	ctx   context.Context
	start func()
	wake  func(func())
}

// Source shares one index sequence and FIFO slot pool between the parent and
// its one-level children. Language state and reactions stay on each own actor.
type runtimeShared struct {
	mu                   sync.Mutex
	index, active, limit int
	queue                []runtimeAdmission
}

func (s *runtimeShared) count() int { s.mu.Lock(); defer s.mu.Unlock(); return s.index }
func (s *runtimeShared) next() (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index >= 1000 {
		return 0, false
	}
	s.index++
	return s.index, true
}
func (s *runtimeShared) admit(entry runtimeAdmission) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active < s.limit {
		s.active++
		return true
	}
	s.queue = append(s.queue, entry)
	return false
}
func (s *runtimeShared) release(actor any) {
	s.mu.Lock()
	for len(s.queue) > 0 {
		entry := s.queue[0]
		s.queue = s.queue[1:]
		if entry.ctx.Err() != nil {
			continue
		}
		s.mu.Unlock()
		if entry.actor == actor {
			entry.start()
		} else {
			entry.wake(entry.start)
		}
		return
	}
	s.active--
	s.mu.Unlock()
}
