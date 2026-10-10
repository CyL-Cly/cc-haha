package sessioncontroller

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"sync"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var ErrPendingClosed = errors.New("pending transcript writer closed")

type WriteFuture struct {
	once      sync.Once
	done      chan struct{}
	err       error
	mu        sync.Mutex
	reactions []*writeReaction
}

func newWriteFuture() *WriteFuture { return &WriteFuture{done: make(chan struct{})} }
func (future *WriteFuture) settle(err error) {
	future.once.Do(func() {
		future.mu.Lock()
		future.err = err
		reactions := future.reactions
		future.reactions = nil
		// Keep registration and settlement ordered: a newly registered reaction
		// cannot overtake earlier reactions while immutable Done is published.
		for _, reaction := range reactions {
			_ = reaction.post(err)
		}
		close(future.done)
		future.mu.Unlock()
	})
}
func (future *WriteFuture) Done() <-chan struct{} { return future.done }
func (future *WriteFuture) Wait(ctx context.Context) error {
	if ctx == nil {
		return errors.New("writer future requires a context")
	}
	// A settled promise retains its result even if the observer cancels later.
	select {
	case <-future.done:
		return future.err
	default:
	}
	select {
	case <-future.done:
		return future.err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Await releases only the original checkpoint stack. It does not cancel the
// independent filesystem job if the caller stops waiting.
func (future *WriteFuture) Await(scope *cooperative.Scope) error {
	if scope == nil {
		return cooperative.ErrRestricted
	}
	done := make(chan struct{})
	if err := future.then(scope, func(*cooperative.Scope, error) { close(done) }); err != nil {
		return err
	}
	if err := scope.AwaitPromise(done); err != nil {
		return err
	}
	return future.Wait(context.Background())
}

type descriptorLease struct {
	file TranscriptFile
	once sync.Once
	err  error
}

func (lease *descriptorLease) close() error {
	lease.once.Do(func() { lease.err = lease.file.Close() })
	return lease.err
}

type openResult struct {
	lease *descriptorLease
	err   error
}
type statResult struct {
	info fs.FileInfo
	err  error
}
type pendingBatch struct {
	path     string
	entries  []queuedEntry
	lease    *descriptorLease
	identity identity
	index    int
	content  []byte
	units    int
}

// PendingWriter implements the single-destination independent drain. All Writer
// state/live object reads occur on Owner. Workers receive only path/descriptor
// leases/immutable bytes, never Scope, Envelope, Factory or queued entries.
// Initial Record/prepare/metadata IO remains synchronous and is not full SDK.
type PendingWriter struct {
	writer          *Writer
	owner           *cooperative.Owner
	active          *WriteFuture
	natural         bool
	closing         bool
	hostClaimed     bool
	closeFuture     *WriteFuture
	workers         sync.WaitGroup
	recordAdmission recordNativeAdmission
	runtimeNative   runtimeNativeReferences
	tracked         int
	zeroWaiters     []*WriteFuture
	uuidCache       *sessionUUIDCache
}

func NewPendingWriter(writer *Writer, owner *cooperative.Owner) (*PendingWriter, error) {
	if writer != nil && writer.options.Bootstrap != nil && !writer.options.Bootstrap.Owns(owner) {
		return nil, cooperative.ErrRestricted
	}
	if writer == nil || owner == nil {
		return nil, errors.New("pending writer requires the real Writer and owner")
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.async != nil {
		return nil, errors.New("writer already has an independent drain owner")
	}
	pending := &PendingWriter{writer: writer, owner: owner}
	if _, err := pending.runtimeNative.refs.Bind(owner); err != nil {
		return nil, err
	}
	writer.async = pending
	return pending, nil
}
func (pending *PendingWriter) postDrain() { pending.postDrainReference(nil) }

func (pending *PendingWriter) postDrainReference(ref *ownerasync.NativeReference) {
	// Timer workers submit only immutable readiness. The same original drain
	// callback releases its timer reference after entry has dispatched actual IO
	// or admitted continuations. It creates no second owner task or Promise layer.
	receipt, err := pending.owner.SubmitIndependent(context.Background(), cooperative.Flush, func(scope *cooperative.Scope) error {
		defer ref.DeliveryFinished()
		if pending.closing {
			return nil
		}
		_, err := pending.begin(scope, true)
		if err != nil {
			pending.writer.options.OnError(err)
		}
		return err
	})
	if err != nil {
		ref.DeliveryFinished()
		return
	}
	if ref != nil {
		// Administrative rejected delivery must also release the timer: an owner
		// can close after accepting readiness but before executing that callback.
		go func() { _ = receipt.Wait(); ref.DeliveryFinished() }()
	}
}
func (pending *PendingWriter) Begin(scope *cooperative.Scope) (*WriteFuture, error) {
	return pending.begin(scope, false)
}
func (pending *PendingWriter) begin(scope *cooperative.Scope, natural bool) (*WriteFuture, error) {
	if !scope.BelongsTo(pending.owner) {
		return nil, cooperative.ErrRestricted
	}
	var future *WriteFuture
	err := scope.Inline(cooperative.Flush, func(*cooperative.Scope) error {
		if pending.closing {
			return ErrPendingClosed
		}
		if pending.active != nil {
			pending.natural = pending.natural || natural
			future = pending.active
			return nil
		}
		pending.writer.mu.Lock()
		defer pending.writer.mu.Unlock()
		// Existing Writer maps do not expose Source Map insertion order. The two
		// original profiles have one destination; multiple targets are a typed cap.
		if len(pending.writer.queues) > 1 {
			return capability("independent multi-target transcript drain order")
		}
		if pending.writer.stop != nil {
			pending.writer.stop()
			pending.writer.stop = nil
		}
		future = newWriteFuture()
		pending.active = future
		pending.natural = natural
		pending.nextLocked()
		return nil
	})
	return future, err
}

// nextLocked consumes the real Writer queues/policy, retaining live references.
func (pending *PendingWriter) nextLocked() {
	for path, entries := range pending.writer.queues {
		if len(entries) == 0 {
			delete(pending.writer.queues, path)
			continue
		}
		pending.writer.queues[path] = nil
		if pending.writer.skipped() {
			pending.writer.discard(entries)
			delete(pending.writer.queues, path)
			continue
		}
		pending.open(&pendingBatch{path: path, entries: entries})
		return
	}
	future := pending.active
	pending.active = nil
	pending.natural = false
	if future != nil {
		future.settle(nil)
	}
}

// dispatch must be called on owner. The worker's only mutable object is its
// private descriptor lease; completion is an explicit owner command.
func dispatch[T any](pending *PendingWriter, future *WriteFuture, work func() T, cleanup func(T), complete func(T)) {
	pending.runtimeNative.begin()
	pending.workers.Add(1)
	go func() {
		defer pending.workers.Done()
		defer pending.runtimeNative.end()
		result := work()
		receipt, err := pending.owner.SubmitIndependent(context.Background(), cooperative.Flush, func(*cooperative.Scope) error { complete(result); return nil })
		if err == nil {
			err = receipt.Wait()
		}
		if err != nil {
			if cleanup != nil {
				cleanup(result)
			}
			future.settle(err)
		}
	}()
}
func (pending *PendingWriter) open(batch *pendingBatch) {
	files, path, future := pending.writer.options.Files, batch.path, pending.active
	dispatch(pending, future, func() openResult {
		file, err := files.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return openResult{err: err}
		}
		return openResult{lease: &descriptorLease{file: file}}
	}, func(result openResult) {
		if result.lease != nil {
			_ = result.lease.close()
		}
	}, func(result openResult) {
		if errors.Is(result.err, fs.ErrNotExist) {
			pending.writer.mu.Lock()
			pending.writer.discard(batch.entries)
			pending.nextLocked()
			pending.writer.mu.Unlock()
			return
		}
		if result.err != nil {
			pending.finish(batch, result.err)
			return
		}
		batch.lease = result.lease
		pending.stat(batch)
	})
}
func (pending *PendingWriter) stat(batch *pendingBatch) {
	lease, future := batch.lease, pending.active
	dispatch(pending, future, func() statResult { info, err := lease.file.Stat(); return statResult{info, err} }, func(statResult) { _ = lease.close() }, func(result statResult) {
		err := result.err
		if err == nil && !result.info.Mode().IsRegular() {
			err = errors.New("Refusing non-regular transcript target: " + batch.path)
		}
		if err == nil {
			batch.identity, err = fileIdentity(result.info)
		}
		if err != nil {
			pending.closeBatch(batch, err)
			return
		}
		pending.encode(batch)
	})
}
func (pending *PendingWriter) encode(batch *pendingBatch) {
	pending.writer.mu.Lock()
	defer pending.writer.mu.Unlock()
	for batch.index < len(batch.entries) {
		item := batch.entries[batch.index]
		batch.index++
		if pending.writer.skipped() || item.identity != batch.identity {
			pending.writer.discard([]queuedEntry{item})
			continue
		}
		encoded, err := pending.writer.marshalLiveEntry(item.entry)
		if err == nil {
			encoded, err = protocol.ParseAndStringifyJavaScriptJSON(encoded)
		}
		if err != nil {
			pending.closeBatch(batch, err)
			return
		}
		line := append(encoded, '\n')
		units := len(utf16.Encode([]rune(string(encoded)))) + 1
		if batch.units+units >= 100*1024*1024 {
			bytes := append([]byte(nil), batch.content...)
			batch.content = nil
			batch.units = 0
			pending.appendBytes(batch, bytes, func() { batch.content = append(batch.content, line...); batch.units = units; pending.encode(batch) })
			return
		}
		batch.content = append(batch.content, line...)
		batch.units += units
	}
	if len(batch.content) > 0 {
		pending.appendBytes(batch, append([]byte(nil), batch.content...), func() { pending.closeBatch(batch, nil) })
		return
	}
	pending.closeBatch(batch, nil)
}
func (pending *PendingWriter) appendBytes(batch *pendingBatch, bytes []byte, complete func()) {
	lease, future := batch.lease, pending.active
	dispatch(pending, future, func() error { return writeAll(lease.file, bytes) }, func(error) { _ = lease.close() }, func(err error) {
		if err != nil {
			pending.closeBatch(batch, err)
			return
		}
		complete()
	})
}
func (pending *PendingWriter) closeBatch(batch *pendingBatch, prior error) {
	lease, future := batch.lease, pending.active
	dispatch(pending, future, func() error { return lease.close() }, nil, func(err error) {
		// Source awaits close in finally; a failed close replaces an earlier
		// stat/encode/append error, while successful close preserves that error.
		if err == nil {
			err = prior
		}
		pending.finish(batch, err)
	})
}
func (pending *PendingWriter) finish(batch *pendingBatch, err error) {
	pending.writer.mu.Lock()
	for _, item := range batch.entries {
		delete(pending.writer.liveEntries, item.entry)
		delete(pending.writer.liveFileHistoryEntries, item.entry)
	}
	if err != nil {
		future, natural := pending.active, pending.natural
		pending.active = nil
		pending.natural = false
		pending.writer.mu.Unlock()
		if natural {
			pending.writer.options.OnError(err)
		}
		future.settle(err)
		return
	}
	// Source deletes only queues that are still empty, retaining late enqueues.
	if len(pending.writer.queues[batch.path]) == 0 {
		delete(pending.writer.queues, batch.path)
	}
	pending.nextLocked()
	pending.writer.mu.Unlock()
}

// Close is off-owner and must run before Owner.Close. It stops admission and
// waits for the independent job plus completion workers; it never inherits a
// model cancellation signal. It does not cancel a blocked native FS operation.
func (pending *PendingWriter) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("pending writer Close requires a context")
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	var future *WriteFuture
	receipt, err := pending.owner.Submit(ctx, cooperative.Flush, func(scope *cooperative.Scope) error {
		if pending.closing {
			future = pending.closeFuture
			return nil
		}
		var beginErr error
		future, beginErr = pending.begin(scope, false)
		if beginErr != nil {
			return beginErr
		}
		pending.stopRecordAdmission()
		pending.closeFuture = future
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	// Once close is admitted, observe settlement/join even if its caller stops
	// waiting or the independent job fails. Model cancellation never owns FS.
	if err == nil {
		err = future.Wait(context.Background())
	}
	pending.workers.Wait()
	return err
}
