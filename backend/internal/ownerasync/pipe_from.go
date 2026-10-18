package ownerasync

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// PipeFrom forwards an internal Promise with one source reaction, matching
// pinned JSC JSPromise::pipeFrom. ResolveFrom instead performs public thenable
// adoption and deliberately has a different reaction graph.
func (future *OwnerFuture[T]) PipeFrom(scope *cooperative.Scope, source *OwnerFuture[T]) error {
	if future == nil || source == nil || source == future || source.owner != future.owner {
		return cooperative.ErrRestricted
	}
	if err := source.check(scope); err != nil {
		return err
	}
	claimed, err := future.claim(scope)
	if err != nil || !claimed {
		return err
	}
	_, err = source.Then(scope, func(_ *cooperative.Scope, value T, failure error) error {
		future.finish(value, failure)
		return nil
	})
	if err != nil {
		var zero T
		future.finish(zero, err)
	}
	return err
}
