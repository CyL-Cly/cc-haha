package sessioncontroller

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// PreparationFuture is the physical prepareTranscriptFile continuation. It is
// distinct from Record's outer promise and from the independent append drain.
// Waiting cancellation does not cancel its native filesystem operations.
type PreparationFuture struct {
	future *WriteFuture
	owner  *cooperative.Owner
}

func (future *PreparationFuture) Done() <-chan struct{} { return future.future.Done() }
func (future *PreparationFuture) Wait(ctx context.Context) error {
	return future.future.Wait(ctx)
}
func (future *PreparationFuture) Await(scope *cooperative.Scope) error {
	if !scope.BelongsTo(future.owner) {
		return cooperative.ErrRestricted
	}
	err := future.future.Await(scope)
	return err
}

// Prepare starts the real mkdir/open/fstat/close chain on the existing Writer's
// owner. Each worker receives only an immutable path or descriptor lease. This
// does not implement recordTranscript's cache, policy filtering, tracked-write
// admission, metadata materialization, Git await, message spread or append.
// A full Record continuation must call this at the corresponding Source await;
// calling it before a synchronous Record is not an equivalent replacement.
func (pending *PendingWriter) Prepare(scope *cooperative.Scope, path string) (*PreparationFuture, error) {
	if !scope.BelongsTo(pending.owner) {
		return nil, cooperative.ErrRestricted
	}
	var future *PreparationFuture
	err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error {
		if pending.closing {
			return ErrPendingClosed
		}
		future = &PreparationFuture{future: newWriteFuture(), owner: pending.owner}
		pending.writer.mu.Lock()
		skipped := pending.writer.skipped()
		files := pending.writer.options.Files
		pending.writer.mu.Unlock()
		if skipped {
			future.future.settle(nil)
			return nil
		}
		directory := filepath.Dir(path)
		dispatchTranscriptNativeOperation(pending, future.future, transcriptNativeFSPMkdir, func() error { return files.MkdirAll(directory, 0700) }, nil, func(err error) {
			if err != nil {
				future.future.settle(err)
				return
			}
			// Source evaluates persistence policy again after the mkdir await.
			pending.writer.mu.Lock()
			skipped := pending.writer.skipped()
			pending.writer.mu.Unlock()
			if skipped {
				future.future.settle(nil)
				return
			}
			pending.openPreparation(future.future, files, path)
		})
		return nil
	})
	return future, err
}

func (pending *PendingWriter) openPreparation(future *WriteFuture, files TranscriptFiles, path string) {
	dispatchTranscriptNativeOperation(pending, future, transcriptNativeFSPOpen, func() openResult {
		file, err := files.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
		if err != nil {
			return openResult{err: err}
		}
		return openResult{lease: &descriptorLease{file: file}}
	}, func(result openResult) {
		if result.lease != nil {
			_ = result.lease.close()
		}
	}, func(result openResult) {
		if result.err != nil {
			future.settle(result.err)
			return
		}
		pending.statPreparation(future, result.lease, path)
	})
}

func (pending *PendingWriter) statPreparation(future *WriteFuture, lease *descriptorLease, path string) {
	dispatch(pending, future, func() statResult {
		info, err := lease.file.Stat()
		return statResult{info, err}
	}, func(statResult) { _ = lease.close() }, func(result statResult) {
		err := result.err
		if err == nil && !result.info.Mode().IsRegular() {
			err = errors.New("Refusing non-regular transcript target: " + path)
		}
		if err == nil {
			var id identity
			id, err = fileIdentity(result.info)
			if err == nil {
				// Source stores identity before awaiting the finally close.
				pending.writer.mu.Lock()
				pending.writer.identities[path] = id
				pending.writer.mu.Unlock()
			}
		}
		pending.closePreparation(future, lease, err)
	})
}

func (pending *PendingWriter) closePreparation(future *WriteFuture, lease *descriptorLease, prior error) {
	dispatchTranscriptNativeOperation(pending, future, transcriptNativeGuardedClose, func() error { return lease.close() }, nil, func(err error) {
		// An error thrown by Source's finally await file.close overrides a stat
		// or non-regular-file error. Successful close retains the earlier error.
		if err == nil {
			err = prior
		}
		future.settle(err)
	})
}
