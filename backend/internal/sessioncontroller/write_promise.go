package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"sync"
)

// Administrative rejection settles only an immutable dependent future. It
// never invokes an owner callback or changes transcript/Set/counter state.
type writeReaction struct {
	owner    *cooperative.Owner
	run      func(*cooperative.Scope, error)
	onClosed func(error)
	once     sync.Once
	done     chan struct{}
}

func (reaction *writeReaction) reject(err error) {
	reaction.once.Do(func() {
		if reaction.onClosed != nil {
			reaction.onClosed(err)
		}
		close(reaction.done)
	})
}
func (reaction *writeReaction) post(err error) error {
	_, rejected := reaction.owner.Microtask(cooperative.Flush, func(scope *cooperative.Scope) error {
		reaction.once.Do(func() { reaction.run(scope, err); close(reaction.done) })
		return nil
	})
	if rejected != nil {
		reaction.reject(rejected)
	}
	return rejected
}
func (future *WriteFuture) thenOwner(owner *cooperative.Owner, run func(*cooperative.Scope, error), rejected ...func(error)) error {
	if owner == nil || run == nil || len(rejected) > 1 {
		return cooperative.ErrRestricted
	}
	reaction := &writeReaction{owner: owner, run: run, done: make(chan struct{})}
	if len(rejected) == 1 {
		reaction.onClosed = rejected[0]
	}
	go func() {
		select {
		case <-reaction.done:
		case <-owner.Done():
			reaction.reject(cooperative.ErrClosed)
		}
	}()
	future.mu.Lock()
	defer future.mu.Unlock()
	select {
	case <-future.done:
		return reaction.post(future.err)
	default:
		future.reactions = append(future.reactions, reaction)
	}
	return nil
}
func (future *WriteFuture) then(scope *cooperative.Scope, run func(*cooperative.Scope, error)) error {
	return scope.RegisterPromiseReaction(func(owner *cooperative.Owner) error { return future.thenOwner(owner, run) })
}
