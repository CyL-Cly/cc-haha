package queryhooks

import (
	"context"
	"time"
)

// Source combinedAbortSignal.cleanup clears its timer and removes upstream
// listeners, without aborting the handed-off signal. Canceling a completed
// command's context instead would kill a real asyncRewake process immediately.
func outsideCommandContext(parent context.Context, duration time.Duration) (context.Context, func()) {
	child, cancel := context.WithCancelCause(context.WithoutCancel(parent))
	// Source createCombinedAbortSignal checks .aborted synchronously before
	// registering a listener. A pre-canceled parent must not briefly admit a
	// native process while the AfterFunc goroutine is still being scheduled.
	if parent.Err() != nil {
		cancel(context.Cause(parent))
		return child, func() {}
	}
	stopParent := context.AfterFunc(parent, func() { cancel(context.Cause(parent)) })
	timer := time.AfterFunc(duration, func() { cancel(context.DeadlineExceeded) })
	return child, func() { timer.Stop(); stopParent() }
}
