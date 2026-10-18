// Package nativemodule preserves the import protocol for actual Go modules
// whose code is already linked. It does not perform or imitate Source file
// loading, transpilation, native I/O or scheduling delays.
package nativemodule

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// Entry retains one actual module's cached capabilities and namespace. The
// concrete caller owns its finite keys, cache and namespace/export authority.
// Every Import creates a distinct public Promise for that cached namespace.
type Entry[K comparable, N comparable] struct {
	owner      *cooperative.Owner
	key        K
	namespace  N
	validate   func(*cooperative.Scope, K, N) error
	fetch      *ownerasync.OwnerFuture[struct{}]
	load       *ownerasync.OwnerFuture[*Entry[K, N]]
	evaluation *ownerasync.OwnerFuture[struct{}]
	failure    error
}

func NewPrelinked[K comparable, N comparable](owner *cooperative.Owner, scope *cooperative.Scope, key K, namespace N, validate func(*cooperative.Scope, K, N) error) (*Entry[K, N], error) {
	var absent N
	if owner == nil || !scope.BelongsTo(owner) || namespace == absent || validate == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return nil, err
	}
	if err := validate(scope, key, namespace); err != nil {
		return nil, err
	}
	entry := &Entry[K, N]{owner: owner, key: key, namespace: namespace, validate: validate}
	var err error
	if entry.fetch, err = ownerasync.NewOwnerFuture[struct{}](owner); err != nil {
		return nil, err
	}
	if entry.load, err = ownerasync.NewOwnerFuture[*Entry[K, N]](owner); err != nil {
		return nil, err
	}
	if entry.evaluation, err = ownerasync.NewOwnerFuture[struct{}](owner); err != nil {
		return nil, err
	}
	if err = entry.fetch.Resolve(scope, struct{}{}); err != nil {
		return nil, err
	}
	if err = entry.load.Resolve(scope, entry); err != nil {
		return nil, err
	}
	if err = entry.evaluation.Resolve(scope, struct{}{}); err != nil {
		return nil, err
	}
	return entry, nil
}

func (entry *Entry[K, N]) check(scope *cooperative.Scope) error {
	var absent N
	if entry == nil || !scope.BelongsTo(entry.owner) || entry.namespace == absent || entry.validate == nil {
		return cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return err
	}
	return entry.validate(scope, entry.key, entry.namespace)
}

type moduleJoin[K comparable, N comparable] struct {
	entry     *Entry[K, N]
	remaining int
	namespace N
	result    *ownerasync.OwnerFuture[N]
}

func (join *moduleJoin[K, N]) settle(scope *cooperative.Scope, namespace N, failure error) error {
	if join == nil || join.entry == nil || join.remaining == 0 || !scope.BelongsTo(join.entry.owner) {
		return cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return err
	}
	var absent N
	if namespace != absent {
		if namespace != join.entry.namespace {
			return cooperative.ErrRestricted
		}
		if err := join.entry.check(scope); err != nil {
			return err
		}
	}
	join.remaining--
	if failure != nil {
		return join.result.Reject(scope, failure)
	}
	if namespace != absent {
		join.namespace = namespace
	}
	if join.remaining == 0 {
		if join.namespace == absent {
			return join.result.Reject(scope, cooperative.ErrRestricted)
		}
		return join.result.Resolve(scope, join.namespace)
	}
	return nil
}

func (entry *Entry[K, N]) Import(scope *cooperative.Scope) (*ownerasync.OwnerFuture[N], error) {
	if err := entry.check(scope); err != nil {
		return nil, err
	}
	loaded, err := entry.loadNamespace(scope)
	if err != nil {
		return nil, err
	}
	// Pinned JSC globalFuncImportModule creates an import-expression Promise
	// outside the loader and resolves it with the pending namespace Promise.
	result, err := ownerasync.NewOwnerFuture[N](entry.owner)
	if err != nil {
		return nil, err
	}
	if err = result.ResolveFrom(scope, loaded); err != nil {
		return nil, err
	}
	return result, nil
}

func (entry *Entry[K, N]) loadNamespace(scope *cooperative.Scope) (*ownerasync.OwnerFuture[N], error) {
	result, err := ownerasync.NewOwnerFuture[N](entry.owner)
	if err != nil {
		return nil, err
	}
	if entry.failure != nil {
		return result, result.Reject(scope, entry.failure)
	}
	intermediate, err := ownerasync.NewOwnerFuture[N](entry.owner)
	if err != nil {
		return nil, err
	}
	topResult, err := ownerasync.NewOwnerFuture[N](entry.owner)
	if err != nil {
		return nil, err
	}
	// ModuleLoadTopSettled starts a fresh state graph and consumes the cached
	// host load in parallel. Neither successful arm alone releases import.
	_, err = entry.fetch.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			return intermediate.Reject(next, failure)
		}
		state, err := ownerasync.NewOwnerFuture[N](entry.owner)
		if err != nil {
			return err
		}
		requested, err := ownerasync.NewOwnerFuture[*Entry[K, N]](entry.owner)
		if err != nil {
			return err
		}
		if err = entry.check(next); err != nil {
			return intermediate.Reject(next, err)
		}
		if err = requested.Resolve(next, entry); err != nil {
			return err
		}
		_, err = requested.Then(next, func(link *cooperative.Scope, record *Entry[K, N], failure error) error {
			if failure != nil {
				return state.Reject(link, failure)
			}
			if record != entry {
				return state.Reject(link, cooperative.ErrRestricted)
			}
			if err := record.check(link); err != nil {
				return state.Reject(link, err)
			}
			_, err := record.evaluation.Then(link, func(evaluated *cooperative.Scope, _ struct{}, failure error) error {
				if failure != nil {
					return state.Reject(evaluated, failure)
				}
				return state.Resolve(evaluated, record.namespace)
			})
			return err
		})
		if err != nil {
			return err
		}
		hostResult, err := ownerasync.NewOwnerFuture[K](entry.owner)
		if err != nil {
			return err
		}
		_, err = entry.load.Then(next, func(loaded *cooperative.Scope, record *Entry[K, N], failure error) error {
			if failure != nil {
				return hostResult.Reject(loaded, failure)
			}
			if record != entry {
				return hostResult.Reject(loaded, cooperative.ErrRestricted)
			}
			if err := record.check(loaded); err != nil {
				return hostResult.Reject(loaded, err)
			}
			return hostResult.Resolve(loaded, record.key)
		})
		if err != nil {
			return err
		}
		if _, err = hostResult.Then(next, func(_ *cooperative.Scope, _ K, failure error) error {
			if failure != nil {
				entry.failure = failure
			}
			return nil
		}); err != nil {
			return err
		}
		combined, err := ownerasync.NewOwnerFuture[N](entry.owner)
		if err != nil {
			return err
		}
		join := &moduleJoin[K, N]{entry: entry, remaining: 2, result: combined}
		var absent N
		if _, err = hostResult.Then(next, func(loaded *cooperative.Scope, _ K, failure error) error {
			return join.settle(loaded, absent, failure)
		}); err != nil {
			return err
		}
		if _, err = state.Then(next, func(evaluated *cooperative.Scope, namespace N, failure error) error {
			return join.settle(evaluated, namespace, failure)
		}); err != nil {
			return err
		}
		return intermediate.PipeFrom(next, combined)
	})
	if err != nil {
		return nil, err
	}
	_, err = intermediate.Then(scope, func(next *cooperative.Scope, namespace N, failure error) error {
		if failure != nil {
			entry.failure = failure
			return topResult.Reject(next, failure)
		}
		return topResult.Resolve(next, namespace)
	})
	if err != nil {
		return nil, err
	}
	_, err = topResult.Then(scope, func(next *cooperative.Scope, namespace N, failure error) error {
		if failure != nil {
			return result.Reject(next, failure)
		}
		if namespace != entry.namespace {
			return result.Reject(next, cooperative.ErrRestricted)
		}
		// The concrete namespaces export no callable then. This is namespace
		// resolve, distinct from the public import-expression adoption above.
		return result.Resolve(next, namespace)
	})
	return result, err
}
