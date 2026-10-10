// Package bootstrapstate holds the CLI's live session state on its existing
// cooperative Owner. It does not construct an Owner or an Agent Harness.
package bootstrapstate

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

type Listener[T any] struct {
	call func(*cooperative.Scope, T) error
}

// NewListener preserves function identity, including repeated subscriptions.
func NewListener[T any](call func(*cooperative.Scope, T) error) *Listener[T] {
	return &Listener[T]{call: call}
}

type Signal[T any] struct {
	owner    *cooperative.Owner
	entries  []*Listener[T]
	emitting int
}

func NewSignal[T any](owner *cooperative.Owner) (*Signal[T], error) {
	if owner == nil {
		return nil, cooperative.ErrRestricted
	}
	return &Signal[T]{owner: owner}, nil
}

func check(scope *cooperative.Scope, owner *cooperative.Owner) error {
	if owner == nil || !scope.BelongsTo(owner) {
		return cooperative.ErrRestricted
	}
	return scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
}

type Unsubscribe func(*cooperative.Scope) error

func (signal *Signal[T]) Subscribe(scope *cooperative.Scope, listener *Listener[T]) (Unsubscribe, error) {
	if signal == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := check(scope, signal.owner); err != nil {
		return nil, err
	}
	if listener == nil || listener.call == nil {
		return nil, cooperative.ErrRestricted
	}
	present := false
	for _, entry := range signal.entries {
		if entry == listener {
			present = true
			break
		}
	}
	if !present {
		signal.entries = append(signal.entries, listener)
	}
	return func(next *cooperative.Scope) error {
		if err := check(next, signal.owner); err != nil {
			return err
		}
		for index, entry := range signal.entries {
			if entry == listener {
				signal.entries[index] = nil
				if signal.emitting == 0 {
					signal.compact()
				}
				return nil
			}
		}
		return nil
	}, nil
}

func (signal *Signal[T]) compact() {
	entries := signal.entries[:0]
	for _, entry := range signal.entries {
		if entry != nil {
			entries = append(entries, entry)
		}
	}
	clear(signal.entries[len(entries):])
	signal.entries = entries
}

// Emit iterates the live insertion-ordered Set. Tombstones preserve active
// iterators: delete/re-add and clear/add append entries visited by this emit.
// A synchronous listener error stops this emit and is returned to its caller.
func (signal *Signal[T]) Emit(scope *cooperative.Scope, value T) error {
	if signal == nil {
		return cooperative.ErrRestricted
	}
	if err := check(scope, signal.owner); err != nil {
		return err
	}
	signal.emitting++
	defer func() {
		signal.emitting--
		if signal.emitting == 0 {
			signal.compact()
		}
	}()
	for index := 0; index < len(signal.entries); index++ {
		listener := signal.entries[index]
		if listener != nil {
			if err := listener.call(scope, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (signal *Signal[T]) Clear(scope *cooperative.Scope) error {
	if signal == nil {
		return cooperative.ErrRestricted
	}
	if err := check(scope, signal.owner); err != nil {
		return err
	}
	clear(signal.entries)
	if signal.emitting == 0 {
		signal.entries = nil
	}
	return nil
}
