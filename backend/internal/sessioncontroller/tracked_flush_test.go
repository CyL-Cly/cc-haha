package sessioncontroller

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func beginTracked(t *testing.T, owner *cooperative.Owner, pending *PendingWriter) *TrackedWrite {
	t.Helper()
	var token *TrackedWrite
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		var err error
		token, err = pending.BeginTrackedWrite(scope)
		return err
	})
	return token
}
func beginTrackedFlush(t *testing.T, owner *cooperative.Owner, pending *PendingWriter) *FlushFuture {
	t.Helper()
	var future *FlushFuture
	pendingCall(t, owner, cooperative.Flush, func(scope *cooperative.Scope) error {
		var err error
		future, err = pending.Flush(scope)
		return err
	})
	return future
}
func waitTrackedJoin(t *testing.T, owner *cooperative.Owner, pending *PendingWriter, future *FlushFuture, count int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		joined := false
		pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
			joined = len(pending.zeroWaiters) >= count
			return nil
		})
		if joined {
			return
		}
		select {
		case <-future.Done():
			t.Fatal("flush returned before the admitted tracked write completed")
		default:
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("flush did not join the actual admitted tracked write")
}
func awaitTrackedFlush(t *testing.T, future *FlushFuture) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return future.Wait(ctx)
}

func TestTrackedFlushWaitsForNativePreparationAndLateEnqueue(t *testing.T) {
	files := newPreparationFiles("stat", "")
	owner, writer, pending := preparationHost(t, files)
	token := beginTracked(t, owner, pending)
	var preparation *PreparationFuture
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		var err error
		preparation, err = pending.Prepare(scope, writer.target())
		return err
	})
	awaitSignal(t, files.held, "native preparation fstat result")
	flushed := beginTrackedFlush(t, owner, pending)
	waitTrackedJoin(t, owner, pending, flushed, 1)
	files.unblock()
	if err := preparation.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	// This fixture starts at insert's post-prepare boundary. It does not pretend
	// that Prepare alone implements Record/cache/Git/message materialization.
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		writer.path = writer.target()
		files.hold = ""
		if err := writer.Append(plainMessage("tracked-late", "late native bytes")); err != nil {
			return err
		}
		return token.Complete(scope)
	})
	if err := awaitTrackedFlush(t, flushed); err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(writer.target())
	if err != nil || !strings.Contains(string(disk), `"uuid":"tracked-late"`) || strings.Count(string(disk), "\n") != 1 {
		t.Fatalf("flush omitted/duplicated the late native append: %q %v", disk, err)
	}
	assertPreparationClosed(t, files, 2)
}

func TestTrackedFlushDoesNotWaitForUntrackedOuterPreparation(t *testing.T) {
	files := newPreparationFiles("open", "")
	owner, writer, pending := preparationHost(t, files)
	var preparation *PreparationFuture
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		var err error
		preparation, err = pending.Prepare(scope, writer.target())
		return err
	})
	awaitSignal(t, files.held, "untracked native open")
	if err := awaitTrackedFlush(t, beginTrackedFlush(t, owner, pending)); err != nil {
		t.Fatal("untracked outer work incorrectly became part of flush", err)
	}
	select {
	case <-preparation.Done():
		t.Fatal("held native preparation was synthesized as completed")
	default:
	}
	files.unblock()
	if err := preparation.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertPreparationClosed(t, files, 1)
}

func TestTrackedFlushFinallyAfterNativeFailureDoesNotPropagateRecordError(t *testing.T) {
	files := newPreparationFiles("stat", "stat-close")
	owner, writer, pending := preparationHost(t, files)
	token := beginTracked(t, owner, pending)
	var preparation *PreparationFuture
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		var err error
		preparation, err = pending.Prepare(scope, writer.target())
		return err
	})
	awaitSignal(t, files.held, "failed native fstat")
	flushed := beginTrackedFlush(t, owner, pending)
	waitTrackedJoin(t, owner, pending, flushed, 1)
	files.unblock()
	if err := preparation.Wait(context.Background()); !errors.Is(err, prepareCloseFault) {
		t.Fatal("Source finally close did not retain its own error", err)
	}
	pendingCall(t, owner, cooperative.Flush, token.Complete)
	if err := awaitTrackedFlush(t, flushed); err != nil {
		t.Fatal("Record's independently owned error leaked into flush", err)
	}
	assertPreparationClosed(t, files, 1)
}

func TestTrackedFlushMultipleObserversAndTokensRetainIndependentPromises(t *testing.T) {
	owner, _, pending := makePending(t, OSFiles{})
	first, second := beginTracked(t, owner, pending), beginTracked(t, owner, pending)
	a, b := beginTrackedFlush(t, owner, pending), beginTrackedFlush(t, owner, pending)
	if a == b || a.Done() == b.Done() {
		t.Fatal("two Source flush calls were coalesced into one promise")
	}
	waitTrackedJoin(t, owner, pending, a, 2)
	pendingCall(t, owner, cooperative.Flush, func(scope *cooperative.Scope) error {
		if err := first.Complete(scope); err != nil {
			return err
		}
		if err := first.Complete(scope); err != nil {
			return err
		}
		if pending.tracked != 1 {
			return errors.New("duplicate finally completion released another write")
		}
		return nil
	})
	select {
	case <-a.Done():
		t.Fatal("flush ignored the second admitted write")
	default:
	}
	pendingCall(t, owner, cooperative.Flush, second.Complete)
	for _, future := range []*FlushFuture{a, b} {
		if err := awaitTrackedFlush(t, future); err != nil {
			t.Fatal(err)
		}
	}
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		if pending.tracked != 0 || len(pending.zeroWaiters) != 0 {
			return errors.New("settled flush left tracked writes or zero resolvers")
		}
		return nil
	})
}

func TestTrackedWriteDoesNotBlockNaturalTimerDrain(t *testing.T) {
	owner, writer, pending := makePending(t, OSFiles{})
	token := beginTracked(t, owner, pending)
	// Invoke the same callback used by Writer's real 100ms scheduler. The
	// scheduling delay is irrelevant to the admitted drain's physical result.
	pending.postDrain()
	var physical *WriteFuture
	pendingCall(t, owner, cooperative.Flush, func(scope *cooperative.Scope) error {
		var err error
		physical, err = pending.Begin(scope)
		return err
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := physical.Wait(ctx); err != nil {
		t.Fatal("natural drain incorrectly waited for an unrelated insert", err)
	}
	disk, err := os.ReadFile(writer.target())
	if err != nil || !strings.Contains(string(disk), `"uuid":"native-first"`) {
		t.Fatalf("natural drain did not perform its physical append: %q %v", disk, err)
	}
	pendingCall(t, owner, cooperative.Flush, token.Complete)
}

func TestTrackedFlushCancelsLateScheduledTimerDuringActiveDrain(t *testing.T) {
	files := &heldFiles{mode: "open", arm: make(chan struct{}), held: make(chan struct{}), release: make(chan struct{})}
	owner, writer, pending := makePending(t, files)
	close(files.arm)
	physical := beginPending(t, owner, pending)
	awaitSignal(t, files.held, "active native drain open")
	stopped := 0
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
		writer.options.Schedule = func(time.Duration, func()) func() { return func() { stopped++ } }
		return writer.Append(plainMessage("after-active", "after active batch"))
	})
	flushed := beginTrackedFlush(t, owner, pending)
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		if stopped != 1 || writer.stop != nil {
			return errors.New("flush failed to clear late timer before awaiting existing drain")
		}
		return nil
	})
	close(files.release)
	if err := physical.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := awaitTrackedFlush(t, flushed); err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(writer.target())
	if err != nil || strings.Count(string(disk), "\n") != 2 || !strings.Contains(string(disk), `"uuid":"after-active"`) {
		t.Fatalf("active/late batches lost or duplicated native bytes: %q %v", disk, err)
	}
}

func TestTrackedFlushObserverCancellationLeavesAdmittedJobRunning(t *testing.T) {
	owner, _, pending := makePending(t, OSFiles{})
	token := beginTracked(t, owner, pending)
	flushed := beginTrackedFlush(t, owner, pending)
	waitTrackedJoin(t, owner, pending, flushed, 1)
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("original caller stopped observing flush")
	cancel(cause)
	if err := flushed.Wait(ctx); !errors.Is(err, cause) {
		t.Fatal("observer cancellation cause changed", err)
	}
	pendingCall(t, owner, cooperative.Flush, token.Complete)
	if err := awaitTrackedFlush(t, flushed); err != nil {
		t.Fatal("observer cancellation poisoned the admitted flush", err)
	}
}

func TestTrackedFlushNativeDrainErrorIsRetained(t *testing.T) {
	files := &pendingFaultFiles{}
	owner, _, pending := makePending(t, files)
	files.arm("write-close")
	flushed := beginTrackedFlush(t, owner, pending)
	if err := awaitTrackedFlush(t, flushed); !errors.Is(err, fs.ErrInvalid) {
		t.Fatal("flush did not preserve Source finally close error", err)
	}
	if files.count() != 1 {
		t.Fatal("failed native drain leaked or closed its lease twice")
	}
}

func TestTrackedFlushModelCancellationEndsAwaitButPreservesTrackedJob(t *testing.T) {
	owner, _, pending := makePending(t, OSFiles{})
	token := beginTracked(t, owner, pending)
	flushed := beginTrackedFlush(t, owner, pending)
	waitTrackedJoin(t, owner, pending, flushed, 1)
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("original model request aborted")
	defer cancel(cause)
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- owner.Run(ctx, func(scope *cooperative.Scope) error {
			close(started)
			return flushed.Await(scope)
		})
	}()
	awaitSignal(t, started, "original model await")
	cancel(cause)
	select {
	case err := <-result:
		if !errors.Is(err, cause) {
			t.Fatal("model cancellation cause changed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled model consumer remained joined to independent tracked write")
	}
	// The independent job's finally callback uses owner lifetime, not the
	// cancelled ASK scope. Final SDK host wiring must retain that distinction.
	pendingCall(t, owner, cooperative.Flush, token.Complete)
	if err := awaitTrackedFlush(t, flushed); err != nil {
		t.Fatal("model cancellation poisoned owner-lifetime flush", err)
	}
}

func TestTrackedFlushOwnerAwaitPumpsOriginalFinallyCompletion(t *testing.T) {
	owner, writer, pending := makePending(t, OSFiles{})
	token := beginTracked(t, owner, pending)
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			future, err := pending.Flush(scope)
			if err != nil {
				return err
			}
			close(started)
			return future.Await(scope)
		})
	}()
	awaitSignal(t, started, "original owner flush await")
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		if err := writer.Append(plainMessage("owner-late", "original owner queued bytes")); err != nil {
			return err
		}
		return token.Complete(scope)
	})
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owner await did not pump the actual finally callback")
	}
	disk, err := os.ReadFile(writer.target())
	if err != nil || !strings.Contains(string(disk), `"uuid":"owner-late"`) {
		t.Fatalf("owner await ended before late bytes reached disk: %q %v", disk, err)
	}
}

func TestTrackedFlushRejectsForeignNilStaleCancelledAndClosedAdmission(t *testing.T) {
	owner, _, pending := makePending(t, OSFiles{})
	token := beginTracked(t, owner, pending)
	flushed := beginTrackedFlush(t, owner, pending)
	foreign := cooperative.New()
	defer foreign.Close()
	for _, other := range []*cooperative.Owner{foreign} {
		pendingCall(t, other, cooperative.Input, func(scope *cooperative.Scope) error {
			if future, err := pending.Flush(scope); future != nil || !errors.Is(err, cooperative.ErrRestricted) {
				return errors.New("foreign flush admission caused effects")
			}
			if next, err := pending.BeginTrackedWrite(scope); next != nil || !errors.Is(err, cooperative.ErrRestricted) {
				return errors.New("foreign tracked write admission caused effects")
			}
			if err := token.Complete(scope); !errors.Is(err, cooperative.ErrRestricted) {
				return errors.New("foreign finally mutated the counter")
			}
			if err := flushed.Await(scope); !errors.Is(err, cooperative.ErrRestricted) {
				return errors.New("foreign scope awaited the original flush")
			}
			return nil
		})
	}
	if _, err := pending.Flush(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	if _, err := pending.BeginTrackedWrite(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	var nilToken *TrackedWrite
	if err := nilToken.Complete(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	var stale *cooperative.Scope
	pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error { stale = scope; return nil })
	if _, err := pending.Flush(stale); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("cancelled original admission")
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		cancel(cause)
		if future, err := pending.Flush(scope); future != nil || !errors.Is(err, cause) {
			return errors.New("cancelled flush was admitted")
		}
		_, err := pending.BeginTrackedWrite(scope)
		return err
	}); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	pendingCall(t, owner, cooperative.Flush, token.Complete)
	if err := awaitTrackedFlush(t, flushed); err != nil {
		t.Fatal(err)
	}
	if err := pending.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
		if _, err := pending.Flush(scope); !errors.Is(err, ErrPendingClosed) {
			return errors.New("closed flush admission succeeded")
		}
		if _, err := pending.BeginTrackedWrite(scope); !errors.Is(err, ErrPendingClosed) {
			return errors.New("closed tracked admission succeeded")
		}
		return nil
	})
}

func TestTrackedFlushForcedOwnerCloseReleasesZeroObserver(t *testing.T) {
	owner := cooperative.New()
	writer := actualPendingWriter(t, owner, t.TempDir(), OSFiles{})
	var pending *PendingWriter
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
		var err error
		pending, err = NewPendingWriter(writer, owner)
		return err
	})
	t.Cleanup(owner.Close)
	_ = beginTracked(t, owner, pending)
	flushed := beginTrackedFlush(t, owner, pending)
	waitTrackedJoin(t, owner, pending, flushed, 1)
	owner.Close()
	awaitSignal(t, owner.Done(), "administrative owner termination")
	if err := awaitTrackedFlush(t, flushed); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("forced owner close lost administrative failure", err)
	}
	joined := make(chan struct{})
	go func() { pending.workers.Wait(); close(joined) }()
	awaitSignal(t, joined, "zero observer worker joined")
	if err := pending.Close(context.Background()); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("closed administrative host falsely succeeded", err)
	}
}
