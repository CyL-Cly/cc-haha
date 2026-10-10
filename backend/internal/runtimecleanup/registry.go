// Package runtimecleanup ports the process cleanup registry. It does not own
// an Agent loop, an Owner, a process exit policy or a transcript flush.
package runtimecleanup

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

type Future = ownerasync.OwnerFuture[struct{}]

// Callback identity is the original function identity used by the Source Set.
// Start enters synchronously on the existing owner. A nil successful future
// represents a synchronous undefined return, accepted by Promise.all too.
type Callback struct {
	start func(*cooperative.Scope) (*Future, error)
}

// NewCallback freezes the function value associated with this identity. The
// body may use live owner state, but registration cannot replace its callable.
func NewCallback(start func(*cooperative.Scope) (*Future, error)) *Callback {
	return &Callback{start: start}
}

type Registry struct {
	owner   *cooperative.Owner
	entries []*Callback
}

func New(owner *cooperative.Owner) (*Registry, error) {
	if owner == nil {
		return nil, cooperative.ErrRestricted
	}
	return &Registry{owner: owner}, nil
}

// Owns checks construction identity without executing a callback or reaction.
func (registry *Registry) Owns(owner *cooperative.Owner) bool {
	return registry != nil && owner != nil && registry.owner == owner
}

func (registry *Registry) check(scope *cooperative.Scope) error {
	if registry == nil || !scope.BelongsTo(registry.owner) {
		return cooperative.ErrRestricted
	}
	return scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
}

type Unregister func(*cooperative.Scope) (bool, error)

// A caller-scope cancellation is an administrative adapter failure. It must
// not leave an admitted cleanup result pending until a later Owner.Close.
func settle(scope *cooperative.Scope, future *Future, failure error) error {
	var err error
	if failure != nil {
		err = future.Reject(scope, failure)
	} else {
		err = future.Resolve(scope, struct{}{})
	}
	if err != nil {
		_ = future.RejectAdministrative(err)
	}
	return err
}

// Register preserves Set insertion order and duplicate function identity.
// Every returned unregister removes that identity, not a registration token.
func (registry *Registry) Register(scope *cooperative.Scope, callback *Callback) (Unregister, error) {
	if err := registry.check(scope); err != nil {
		return nil, err
	}
	if callback == nil || callback.start == nil {
		return nil, cooperative.ErrRestricted
	}
	present := false
	for _, entry := range registry.entries {
		if entry == callback {
			present = true
			break
		}
	}
	if !present {
		registry.entries = append(registry.entries, callback)
	}
	return func(next *cooperative.Scope) (bool, error) {
		if err := registry.check(next); err != nil {
			return false, err
		}
		for index, entry := range registry.entries {
			if entry == callback {
				copy(registry.entries[index:], registry.entries[index+1:])
				registry.entries[len(registry.entries)-1] = nil
				registry.entries = registry.entries[:len(registry.entries)-1]
				return true, nil
			}
		}
		return false, nil
	}, nil
}

// Start implements await Promise.all(Array.from(set).map(fn => fn())). Every
// original callback starts before any returned Promise is observed. Snapshot
// mutations affect the next run. A synchronous throw stops map; an asynchronous
// rejection does not stop later callbacks or cancel their independent work.
func (registry *Registry) Start(scope *cooperative.Scope) (*Future, error) {
	if err := registry.check(scope); err != nil {
		return nil, err
	}
	result, _ := ownerasync.NewOwnerFuture[struct{}](registry.owner)
	snapshot := append([]*Callback(nil), registry.entries...)
	values := make([]*Future, 0, len(snapshot))
	for _, callback := range snapshot {
		var value *Future
		failure := scope.Inline(cooperative.Flush, func(next *cooperative.Scope) error {
			var err error
			value, err = callback.start(next)
			return err
		})
		if failure != nil {
			return result, settle(scope, result, failure)
		}
		values = append(values, value)
	}

	all, _ := ownerasync.NewOwnerFuture[struct{}](registry.owner)
	remaining := len(values)
	if remaining == 0 {
		if err := settle(scope, all, nil); err != nil {
			_ = result.RejectAdministrative(err)
			return result, err
		}
	}
	for _, value := range values {
		if value == nil {
			value, _ = ownerasync.NewOwnerFuture[struct{}](registry.owner)
			if err := settle(scope, value, nil); err != nil {
				_ = result.RejectAdministrative(err)
				return result, err
			}
		}
		_, err := value.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
			if failure != nil {
				return settle(next, all, failure)
			}
			remaining--
			if remaining == 0 {
				return settle(next, all, nil)
			}
			return nil
		})
		if err != nil {
			if administrative := settle(scope, all, err); administrative != nil {
				_ = result.RejectAdministrative(administrative)
				return result, administrative
			}
		}
	}
	_, err := all.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		return settle(next, result, failure)
	})
	if err != nil {
		_ = settle(scope, result, err)
		return result, err
	}
	return result, nil
}
