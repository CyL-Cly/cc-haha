package sessioncontroller

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

// SessionUUIDFuture is the memoized cache promise before Record's tracked
// insert. It is not a Record receipt and never primes Writer.members itself.
type SessionUUIDFuture struct {
	future *WriteFuture
	owner  *cooperative.Owner
	set    *sessionhistory.MessageSet
}

func (future *SessionUUIDFuture) Done() <-chan struct{} { return future.future.Done() }
func (future *SessionUUIDFuture) Wait(ctx context.Context) (*sessionhistory.MessageSet, error) {
	if err := future.future.Wait(ctx); err != nil {
		return nil, err
	}
	return future.set, nil
}
func (future *SessionUUIDFuture) Await(scope *cooperative.Scope) (*sessionhistory.MessageSet, error) {
	if !scope.BelongsTo(future.owner) {
		return nil, cooperative.ErrRestricted
	}
	err := future.future.Await(scope)
	if err != nil {
		return nil, err
	}
	return future.set, nil
}

type sessionUUIDCache struct {
	loader   *sessionhistory.Loader
	promises map[string]*SessionUUIDFuture
}
type readLease struct {
	file sessionstore.File
	once sync.Once
	err  error
}

func (lease *readLease) close() error {
	lease.once.Do(func() { lease.err = lease.file.Close() })
	return lease.err
}

type uuidPhysicalResult struct {
	result sessionhistory.PhysicalReadResult
	lease  *readLease
}
type uuidJob struct {
	pending    *PendingWriter
	loader     *sessionhistory.Loader
	reader     *sessionhistory.ReadContinuation
	io         sessionhistory.PhysicalIO
	future     *SessionUUIDFuture
	lease      *readLease
	loaded     *sessionhistory.Transcript
	loadReturn *WriteFuture
}

// SessionUUIDs captures the original current-project directory and memoizes by
// SID like Source. Clear removes cache entries without canceling pending loads.
// This finite SID input is well-formed UTF8 without path separators; arbitrary
// JS-valued/lone-UTF16/path-traversing SIDs require the actual host resolver.
func (pending *PendingWriter) SessionUUIDs(scope *cooperative.Scope, loader *sessionhistory.Loader, sid string) (*SessionUUIDFuture, error) {
	if !scope.BelongsTo(pending.owner) {
		return nil, cooperative.ErrRestricted
	}
	if loader == nil {
		return nil, errors.New("session UUID load requires the real history loader")
	}
	if sid == "" || !utf8.ValidString(sid) || strings.ContainsAny(sid, "/\\\x00") {
		return nil, capability("original session SID UTF8 leaf")
	}
	var future *SessionUUIDFuture
	err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error {
		if pending.closing {
			return ErrPendingClosed
		}
		if pending.uuidCache == nil {
			pending.uuidCache = &sessionUUIDCache{loader: loader, promises: map[string]*SessionUUIDFuture{}}
		}
		if pending.uuidCache.loader != loader {
			return errors.New("session UUID cache already belongs to another loader")
		}
		if found := pending.uuidCache.promises[sid]; found != nil {
			future = found
			return nil
		}
		pending.writer.mu.Lock()
		path := filepath.Join(filepath.Dir(pending.writer.target()), sid+".jsonl")
		if pending.writer.options.AgentID != "" {
			path = pending.writer.target()
		}
		pending.writer.mu.Unlock()
		reader, err := loader.BeginRead(path, false)
		if err != nil {
			return err
		}
		future = &SessionUUIDFuture{future: newWriteFuture(), owner: pending.owner}
		pending.uuidCache.promises[sid] = future
		job := &uuidJob{pending: pending, loader: loader, reader: reader, io: loader.PhysicalIO(), future: future}
		job.loadReturn, err = job.beginLoadReturn(scope)
		if err != nil {
			future.future.settle(err)
			return nil
		}
		job.next()
		return nil
	})
	return future, err
}
func (pending *PendingWriter) ClearSessionUUIDCache(scope *cooperative.Scope) error {
	if !scope.BelongsTo(pending.owner) {
		return cooperative.ErrRestricted
	}
	return scope.Inline(cooperative.Read, func(*cooperative.Scope) error {
		if pending.closing {
			return ErrPendingClosed
		}
		if pending.uuidCache != nil {
			clear(pending.uuidCache.promises)
		}
		return nil
	})
}
func (job *uuidJob) next() {
	request := job.reader.Request()
	primitive := job.reader.NativePrimitive()
	if request.Operation == sessionhistory.ReadComplete {
		value, err := job.loader.FinishRead(job.reader)
		if job.pending.observeUUIDLoadReturn != nil {
			job.pending.observeUUIDLoadReturn()
		}
		job.loaded = value
		job.loadReturn.settle(err)
		return
	}
	operation := transcriptNativeUnwrapped
	if job.pending.sdkAsyncObservers != nil {
		var err error
		operation, err = transcriptReadNativeOperation(request.Operation, primitive)
		if err != nil {
			job.future.future.settle(err)
			return
		}
	}
	io, lease := job.io, job.lease
	dispatchTranscriptNativeOperation(job.pending, job.future.future, operation, func() uuidPhysicalResult {
		result := uuidPhysicalResult{lease: lease}
		switch request.Operation {
		case sessionhistory.ReadStat:
			result.result.Size, result.result.Error = io.Stat(request.Path)
		case sessionhistory.ReadWhole:
			result.result.Bytes, result.result.Error = io.ReadFile(request.Path)
		case sessionhistory.ReadOpen:
			file, err := io.Files.Open(request.Path)
			result.result.Error = err
			if err == nil {
				result.lease = &readLease{file: file}
			}
		case sessionhistory.ReadAt:
			chunk := make([]byte, request.Length)
			n, err := lease.file.ReadAt(chunk, request.Offset)
			result.result.Bytes, result.result.Error = chunk[:n], err
		case sessionhistory.ReadClose:
			result.result.Error = lease.close()
			result.lease = nil
		}
		return result
	}, func(result uuidPhysicalResult) {
		if result.lease != nil {
			_ = result.lease.close()
		}
	}, func(result uuidPhysicalResult) {
		job.lease = result.lease
		if err := job.reader.Complete(result.result); err != nil {
			// Only invalid external I/O responses can fail Complete. Close any
			// still-adopted descriptor before reporting that adapter failure.
			if job.lease == nil {
				job.future.future.settle(err)
				return
			}
			lease := job.lease
			dispatch(job.pending, job.future.future, func() error { return lease.close() }, nil, func(error) { job.future.future.settle(err) })
			return
		}
		job.next()
	})
}

func transcriptReadNativeOperation(operation sessionhistory.ReadOperation, primitive sessionhistory.ReadPrimitive) (transcriptNativeOperation, error) {
	switch primitive {
	case sessionhistory.ReadPrimitiveFSFunction:
		switch operation {
		case sessionhistory.ReadStat:
			return transcriptNativeFSPStat, nil
		case sessionhistory.ReadWhole:
			return transcriptNativeFSPReadFile, nil
		case sessionhistory.ReadOpen:
			return transcriptNativeFSPOpen, nil
		}
	case sessionhistory.ReadPrimitiveFileHandleClose:
		if operation == sessionhistory.ReadClose {
			return transcriptNativeGuardedClose, nil
		}
	case sessionhistory.ReadPrimitiveFileHandle:
		if operation == sessionhistory.ReadAt {
			return transcriptNativeUnwrapped, nil
		}
	case sessionhistory.ReadPrimitiveStream:
		if operation == sessionhistory.ReadOpen || operation == sessionhistory.ReadAt || operation == sessionhistory.ReadClose {
			return transcriptNativeUnwrapped, nil
		}
	}
	return transcriptNativeUnwrapped, cooperative.ErrRestricted
}
