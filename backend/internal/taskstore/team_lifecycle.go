package taskstore

import (
	"sync"
	"sync/atomic"
)

type heldTeamLifecycle struct {
	list       string
	active     atomic.Bool
	operations sync.RWMutex
}

// WithTeamLifecycleLock is the original withTaskListLifecycleLock reader
// transaction used by plan/config mutations. It does not grant a queued task
// writer permission to join a recreated or deleted generation.
func (s *Service) WithTeamLifecycleLock(list string, run func() error) error {
	return s.withLifecycle(list, false, func(lifecycle) error { return run() })
}

// WithTeamLifecycleTransaction preserves Source's AsyncLocalStorage reentrancy
// while using an explicit scoped service in Go. Only this active, same-list
// handle can execute existing task operations without reacquiring its lease.
// Ordinary services and a retained handle after return keep all original gates.
func (s *Service) WithTeamLifecycleTransaction(list string, run func(*Service) error) error {
	return s.withLifecycle(list, false, func(lifecycle) error {
		transaction := &heldTeamLifecycle{list: list}
		transaction.active.Store(true)
		defer func() {
			transaction.operations.Lock()
			transaction.active.Store(false)
			transaction.operations.Unlock()
		}()
		s.mu.Lock()
		precision := s.precision
		s.mu.Unlock()
		child := &Service{options: s.options, precision: precision, teamTransaction: transaction}
		return run(child)
	})
}
