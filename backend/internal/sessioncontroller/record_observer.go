package sessioncontroller

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// ObserveDetached registers a process-owned reaction for a fire-and-forget
// Record. It does not await the Record, join Project, or change its error.
// Administrative owner shutdown cannot execute a business callback.
func (future *RecordFuture) ObserveDetached(scope *cooperative.Scope, settled func(*cooperative.Scope, error)) error {
	if future == nil || future.future == nil || future.owner == nil || scope == nil || !scope.BelongsTo(future.owner) || settled == nil {
		return cooperative.ErrRestricted
	}
	return future.future.then(scope, settled)
}
