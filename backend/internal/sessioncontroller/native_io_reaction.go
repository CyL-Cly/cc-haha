package sessioncontroller

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// completeTranscriptNativeIO is called at real syscall readiness on Owner.
// prepareTranscriptFile/enqueueWrite/drainWriteQueue await the native Promise:
// readiness settles it, and its registered reaction consumes the same result.
// Keep the worker's native reference through that reaction, with no new I/O.
func completeTranscriptNativeIO(scope *cooperative.Scope, pending *PendingWriter, complete func(*cooperative.Scope)) (*cooperative.Receipt, error) {
	if pending == nil || !scope.BelongsTo(pending.owner) || complete == nil {
		return nil, cooperative.ErrRestricted
	}
	return pending.owner.Microtask(cooperative.Flush, func(next *cooperative.Scope) error {
		complete(next)
		return nil
	})
}

// These names describe the actual Source callsite, not the Go syscall. In the
// fixture, fs/promises functions and an opened FileHandle's close are guarded
// by async functions which await the native Promise and return its plain value.
// FileHandle.stat/appendFile and callback-based streams have no such wrapper.
type transcriptNativeOperation uint8

const (
	transcriptNativeUnwrapped transcriptNativeOperation = iota
	transcriptNativeFSPMkdir
	transcriptNativeFSPOpen
	transcriptNativeFSPStat
	transcriptNativeFSPReadFile
	transcriptNativeGuardedClose
)

type transcriptNativeCompletion interface {
	Wait() error
}

// The worker retains its existing native reference until BOTH the guard await
// and the real consumer have returned. The first receipt publishes consumer
// before it closes, so Wait needs no new worker, timer or synchronization job.
type guardedTranscriptNativeCompletion struct {
	guard    *cooperative.Receipt
	consumer *cooperative.Receipt
}

func (completion *guardedTranscriptNativeCompletion) Wait() error {
	if err := completion.guard.Wait(); err != nil {
		return err
	}
	return completion.consumer.Wait()
}

func completeTranscriptNativeOperation(scope *cooperative.Scope, pending *PendingWriter, operation transcriptNativeOperation, complete func(*cooperative.Scope)) (transcriptNativeCompletion, error) {
	if pending == nil || !scope.BelongsTo(pending.owner) || complete == nil {
		return nil, cooperative.ErrRestricted
	}
	if operation > transcriptNativeGuardedClose {
		return nil, cooperative.ErrRestricted
	}
	binding := pending.sdkAsyncObservers
	if binding == nil || operation == transcriptNativeUnwrapped {
		// Preserve the ordinary receipt and its one existing await reaction.
		return completeTranscriptNativeIO(scope, pending, complete)
	}
	if binding.controller == nil || binding.writer != pending.writer || binding.owner != pending.owner || binding.controller.writer != pending.writer || binding.controller.sdkAsyncObservers != binding || binding.controller.options.Bootstrap != nil && !binding.controller.options.Bootstrap.Owns(binding.owner) {
		return nil, cooperative.ErrRestricted
	}
	completion := &guardedTranscriptNativeCompletion{}
	guard, err := pending.owner.Microtask(cooperative.Flush, func(next *cooperative.Scope) error {
		// await nativePromise resumed the actual fixture guard. Its plain-value
		// return (or same rejection) settles the guard's own Promise; only then
		// may the original prepare/append/read consumer resume its await.
		var err error
		completion.consumer, err = completeTranscriptNativeIO(next, pending, complete)
		return err
	})
	if err != nil {
		return nil, err
	}
	completion.guard = guard
	return completion, nil
}
