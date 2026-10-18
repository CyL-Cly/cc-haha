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

// pendingDrain retains the immutable original physical drain obligation. A
// different explicit flush cannot change this job's future or error domain.
type pendingDrain struct {
	future       *WriteFuture
	natural      bool
	queueStarted bool
	queueCursor  *transcriptQueueGeneration
}

type pendingBatch struct {
	job      *pendingDrain
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
	// The actual Source fixture's async filesystem guards, attached only by the
	// existing Controller observer binding. Ordinary product construction is nil.
	sdkAsyncObservers *sdkAsyncReturnObservers
	// Optional read-only inline diagnostic. Nil in normal product construction.
	observeWriterState func(writerObservation)
	// Optional read-only observation of the actual transcript reducer return.
	// It supplies no value, Scope, readiness or replacement I/O; nil in production.
	observeUUIDLoadReturn func()
	writer                *Writer
	owner                 *cooperative.Owner
	// active is only the timer-owned drain observed by Source Project.flush.
	active *WriteFuture
	// drains is admission order of actual in-flight physical drain jobs, never
	// outer Flush/Record/tracked-zero or pure branch Promise observers.
	drains          []*pendingDrain
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

// Begin retains the existing physical-drain observer API. Existing SDKhost
// tests use it to observe the original pending D rather than create another
// Source call. Production explicit flush and metadata ACK use beginExplicit.
func (pending *PendingWriter) Begin(scope *cooperative.Scope) (*WriteFuture, error) {
	return pending.begin(scope, false)
}

type drainAdmission uint8

const (
	observePhysicalDrain drainAdmission = iota
	naturalTimerDrain
	explicitPhysicalDrain
	closingPhysicalDrain
)

func (pending *PendingWriter) begin(scope *cooperative.Scope, natural bool) (*WriteFuture, error) {
	if natural {
		return pending.admitDrain(scope, naturalTimerDrain)
	}
	return pending.admitDrain(scope, observePhysicalDrain)
}
func (pending *PendingWriter) beginExplicit(scope *cooperative.Scope) (*WriteFuture, error) {
	return pending.admitDrain(scope, explicitPhysicalDrain)
}
func (pending *PendingWriter) admitDrain(scope *cooperative.Scope, admission drainAdmission) (*WriteFuture, error) {
	if !scope.BelongsTo(pending.owner) {
		return nil, cooperative.ErrRestricted
	}
	var future *WriteFuture
	err := scope.Inline(cooperative.Flush, func(*cooperative.Scope) error {
		if pending.closing && admission != closingPhysicalDrain {
			return ErrPendingClosed
		}
		natural := admission == naturalTimerDrain
		if admission == observePhysicalDrain || natural {
			if natural {
				// Preserve the existing natural-to-natural admission policy. An explicit
				// drain is never installed in this timer-only slot or given its errors.
				if pending.active != nil {
					future = pending.active
					return nil
				}
			} else {
				// Select the earliest still-pending real job, with no compatibility-only
				// active slot and no resurrection of a completed historical error.
				for _, job := range pending.drains {
					select {
					case <-job.future.Done():
					default:
						future = job.future
						return nil
					}
				}
			}
		}
		pending.writer.mu.Lock()
		defer pending.writer.mu.Unlock()
		if pending.writer.stop != nil {
			pending.writer.stop()
			pending.writer.stop = nil
		}
		future = newWriteFuture()
		job := &pendingDrain{future: future, natural: natural}
		pending.drains = append(pending.drains, job)
		if natural {
			pending.active = future
		}
		pending.nextLocked(job)
		return nil
	})
	return future, err
}

// removeDrainLocked drops only this completed job's administrative registry
// entry. It cannot clear or settle a different timer/manual drain.
func (pending *PendingWriter) removeDrainLocked(job *pendingDrain) {
	for index, current := range pending.drains {
		if current == job {
			copy(pending.drains[index:], pending.drains[index+1:])
			pending.drains[len(pending.drains)-1] = nil
			pending.drains = pending.drains[:len(pending.drains)-1]
			break
		}
	}
	if job.natural && pending.active == job.future {
		pending.active = nil
	}
	if len(pending.drains) == 0 {
		pending.writer.compactQueueGenerationsLocked()
	}
}

// nextLocked advances this drain's original Map-entry iterator once per entry.
// The same entry's late enqueue remains for a later drain. A newly inserted key
// or a deleted/reinserted generation remains visible after the current cursor.
func (pending *PendingWriter) nextLocked(job *pendingDrain) {
	for {
		path, entries, exists := job.nextQueueGenerationLocked(pending.writer)
		if !exists {
			break
		}
		if len(entries) == 0 {
			continue
		}
		pending.writer.queues[path] = nil
		if pending.writer.skipped() {
			pending.writer.discard(entries)
			continue
		}
		pending.open(&pendingBatch{job: job, path: path, entries: entries})
		return
	}
	// Source only runs this second cleanup loop after the whole main iterator
	// succeeds. Per-batch deletion would create a different Map generation.
	pending.writer.deleteEmptyQueuesLocked()
	pending.removeDrainLocked(job)
	job.future.settle(nil)
}

// dispatch must be called on owner. The worker's only mutable object is its
// private descriptor lease; completion is an explicit owner command.
func dispatch[T any](pending *PendingWriter, future *WriteFuture, work func() T, cleanup func(T), complete func(T)) {
	dispatchTranscriptNativeOperation(pending, future, transcriptNativeUnwrapped, work, cleanup, complete)
}

func dispatchTranscriptNativeOperation[T any](pending *PendingWriter, future *WriteFuture, operation transcriptNativeOperation, work func() T, cleanup func(T), complete func(T)) {
	pending.runtimeNative.begin()
	pending.workers.Add(1)
	go func() {
		defer pending.workers.Done()
		defer pending.runtimeNative.end()
		result := work()
		var reaction transcriptNativeCompletion
		receipt, err := pending.owner.SubmitIndependent(context.Background(), cooperative.Flush, func(scope *cooperative.Scope) error {
			var err error
			reaction, err = completeTranscriptNativeOperation(scope, pending, operation, func(*cooperative.Scope) { complete(result) })
			return err
		})
		if err == nil {
			err = receipt.Wait()
		}
		if err == nil {
			err = reaction.Wait()
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
	files, path, future := pending.writer.options.Files, batch.path, batch.job.future
	dispatchTranscriptNativeOperation(pending, future, transcriptNativeFSPOpen, func() openResult {
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
			pending.nextLocked(batch.job)
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
	lease, future := batch.lease, batch.job.future
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
	lease, future := batch.lease, batch.job.future
	dispatch(pending, future, func() error { return writeAll(lease.file, bytes) }, func(error) { _ = lease.close() }, func(err error) {
		if err != nil {
			pending.closeBatch(batch, err)
			return
		}
		complete()
	})
}
func (pending *PendingWriter) closeBatch(batch *pendingBatch, prior error) {
	lease, future := batch.lease, batch.job.future
	dispatchTranscriptNativeOperation(pending, future, transcriptNativeGuardedClose, func() error { return lease.close() }, nil, func(err error) {
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
		job := batch.job
		pending.removeDrainLocked(job)
		pending.writer.mu.Unlock()
		if job.natural {
			pending.writer.options.OnError(err)
		}
		job.future.settle(err)
		return
	}
	pending.nextLocked(batch.job)
	pending.writer.mu.Unlock()
}

// pendingCloseDrain is an administrative join of real drain jobs, never a
// registry entry itself and never a public Flush/Record/tracked-zero obligation.
// It starts its final drain only after the already-admitted jobs have settled,
// preserving held-batch/late-queue ownership. Workers are joined off-owner.
type pendingCloseDrain struct {
	pending *PendingWriter
	future  *WriteFuture
	waiting []*WriteFuture
	index   int
	failure error
	final   bool
}

func (job *pendingCloseDrain) next(scope *cooperative.Scope) {
	if job.index < len(job.waiting) {
		future := job.waiting[job.index]
		job.index++
		job.wait(future, false)
		return
	}
	if !job.final {
		job.final = true
		drain, err := job.pending.admitDrain(scope, closingPhysicalDrain)
		if err != nil {
			job.finish(err)
			return
		}
		job.wait(drain, true)
	}
}
func (job *pendingCloseDrain) wait(future *WriteFuture, final bool) {
	err := future.thenOwner(job.pending.owner, func(scope *cooperative.Scope, failure error) {
		if job.failure == nil {
			job.failure = failure
		}
		if final {
			job.finish(nil)
			return
		}
		job.next(scope)
	}, job.future.settle)
	if err != nil {
		job.future.settle(err)
	}
}
func (job *pendingCloseDrain) finish(failure error) {
	if job.failure == nil {
		job.failure = failure
	}
	job.future.settle(job.failure)
}

// Close is off-owner and must run before Owner.Close. Its admission fence and
// physical job snapshot run together on Owner. Completed historical errors are
// absent; each public drain future keeps its original result. Administrative
// rejection settles only this Close receipt, and never replaces workers.Wait.
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
		future = newWriteFuture()
		job := &pendingCloseDrain{pending: pending, future: future}
		for _, drain := range pending.drains {
			select {
			case <-drain.future.Done():
			default:
				job.waiting = append(job.waiting, drain.future)
			}
		}
		pending.closeFuture = future
		pending.stopRecordAdmission()
		job.next(scope)
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	// Once close is admitted, observe its independent result despite caller
	// cancellation. An error must never abandon another actual native worker.
	if err == nil {
		err = future.Wait(context.Background())
	}
	pending.workers.Wait()
	return err
}
