package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
)

func admissionClosing(t *testing.T, pending *PendingWriter) {
	t.Helper()
	closed := make(chan struct{})
	var observe func(*cooperative.Scope) error
	observe = func(*cooperative.Scope) error {
		if pending.closing {
			close(closed)
			return nil
		}
		_, err := pending.owner.SubmitIndependent(context.Background(), cooperative.Read, observe)
		return err
	}
	if _, err := pending.owner.SubmitIndependent(context.Background(), cooperative.Read, observe); err != nil {
		t.Fatal(err)
	}
	admissionSignal(t, closed, "actual administrative Close gate")
}

func TestRecordAdmissionDirectClosedDispatch(t *testing.T) {
	owner, _, pending, _ := recordHost(t, OSFiles{}, nil)
	if err := pending.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	var ran, delivered atomic.Bool
	future := newWriteFuture()
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		err := dispatchRecord(pending, future, func() error { ran.Store(true); return nil }, func(*cooperative.Scope, error) { delivered.Store(true) })
		if err != ErrPendingClosed {
			t.Errorf("actual native admission returned %v", err)
		}
		// Production Record uses finish(scope, err) to settle its same future and
		// finally token; a bare caller owns the same returned-error obligation.
		future.settle(err)
		return nil
	})
	if future.Wait(context.Background()) != ErrPendingClosed || ran.Load() || delivered.Load() {
		t.Fatal("closed admission started a syscall/completion or lost rejection")
	}
	count, _ := pending.runtimeNative.snapshot()
	if count != 0 {
		t.Fatalf("closed admission acquired %d native references", count)
	}
}

func TestRecordAdmissionKeepsAdmittedPipeReadAndReceipt(t *testing.T) {
	owner, _, pending, _ := recordHost(t, OSFiles{}, nil)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { _, _ = w.Write([]byte{77}) }) }
	defer release()
	entered, delivered := make(chan struct{}), make(chan struct{})
	future := newWriteFuture()
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		return dispatchRecord(pending, future, func() error {
			close(entered)
			var b [1]byte
			_, err := io.ReadFull(r, b[:])
			if err == nil && b[0] != 77 {
				return errors.New("actual pipe payload lost")
			}
			return err
		}, func(_ *cooperative.Scope, errorValue error) { future.settle(errorValue); close(delivered) })
	})
	admissionSignal(t, entered, "real admitted pipe read")
	result := make(chan error, 1)
	go func() { result <- pending.Close(context.Background()) }()
	admissionClosing(t, pending)
	select {
	case err := <-result:
		t.Fatalf("Close returned before admitted worker release: %v", err)
	default:
	}
	release()
	admissionSignal(t, delivered, "actual admitted owner completion")
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := future.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	count, _ := pending.runtimeNative.snapshot()
	if count != 0 {
		t.Fatalf("native receipt leaked %d refs", count)
	}
}

// This external primitive is an actual same-owner WriteFuture reaction. It
// represents only the declared immutable branch-value/error seam, not a fake
// Git filesystem implementation or Source result.
type admissionOwnedBranch struct {
	owner  *cooperative.Owner
	future *WriteFuture
	value  string
}

func (branch *admissionOwnedBranch) Done() <-chan struct{} { return branch.future.Done() }
func (branch *admissionOwnedBranch) Wait(ctx context.Context) (string, error) {
	return branch.value, branch.future.Wait(ctx)
}
func (branch *admissionOwnedBranch) ThenOwner(scope *cooperative.Scope, run func(*cooperative.Scope, string, error), rejected func(error)) error {
	if !scope.BelongsTo(branch.owner) {
		return cooperative.ErrRestricted
	}
	return branch.future.thenOwner(branch.owner, func(scope *cooperative.Scope, err error) { run(scope, branch.value, err) }, rejected)
}

func TestRecordAdmissionPureLegacyBranchDoesNotHoldAdministrativeClose(t *testing.T) {
	owner, writer, pending, loader := recordHost(t, OSFiles{}, nil)
	branch := &heldRecordBranch{done: make(chan struct{}), value: "original external branch"}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(branch.done) })
	entered := make(chan struct{})
	future := startRecord(t, owner, pending, loader, []LiveRecord{{Record: plainMessage("pure-branch", "original pending prompt")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }, Branch: func(scope *cooperative.Scope) (RecordBranchFuture, error) {
		if !scope.BelongsTo(owner) {
			return nil, cooperative.ErrRestricted
		}
		close(entered)
		return branch, nil
	}})
	admissionSignal(t, entered, "actual original branch callback")
	if err := pending.WaitRuntimeNative(context.Background()); err != nil {
		t.Fatal(err)
	}
	closeReturned := make(chan struct{})
	var closeError error
	go func() { closeError = pending.Close(context.Background()); close(closeReturned) }()
	admissionSignal(t, closeReturned, "Close without fulfilling original pure branch promise")
	if closeError != nil {
		t.Fatal(closeError)
	}
	select {
	case <-branch.Done():
		t.Fatal("Close forced original pure Branch future")
	default:
	}
	if _, err := future.Wait(context.Background()); err != ErrPendingClosed {
		t.Fatalf("late pure branch Record: %v", err)
	}
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		if pending.tracked != 0 {
			t.Errorf("Record finally lost tracked token %d", pending.tracked)
		}
		return nil
	})
	if data := recordBytes(t, writer); string(data) != "" {
		t.Fatal("closed Record appended unexpected transcript", data)
	}
}

func TestRecordAdmissionOwnedBranchKeepsFatalIdentityAfterClose(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, cooperative.ErrRestricted, cooperative.ErrClosed, capability("original branch capability"), &gitbranch.CapabilityError{Boundary: "original owned Git capability"}} {
		t.Run(cause.Error(), func(t *testing.T) {
			owner, _, pending, loader := recordHost(t, OSFiles{}, nil)
			branch := &admissionOwnedBranch{owner: owner, future: newWriteFuture(), value: "original external branch"}
			defer branch.future.settle(cooperative.ErrClosed)
			entered := make(chan struct{})
			future := startRecord(t, owner, pending, loader, []LiveRecord{{Record: plainMessage("fatal-branch", "original prompt")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }, Branch: func(scope *cooperative.Scope) (RecordBranchFuture, error) {
				if !scope.BelongsTo(owner) {
					return nil, cooperative.ErrRestricted
				}
				close(entered)
				return branch, nil
			}})
			admissionSignal(t, entered, "actual owned branch admission")
			observed := make(chan error, 1)
			pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
				return future.ObserveDetached(s, func(s *cooperative.Scope, e error) {
					if !s.BelongsTo(owner) {
						t.Error("detached receipt changed Owner")
					}
					observed <- e
				})
			})
			if err := pending.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-future.Done():
				t.Fatal("Close settled pure owned branch Record early")
			default:
			}
			branch.future.settle(cause)
			if _, err := future.Wait(context.Background()); err != cause {
				t.Fatalf("original fatal identity replaced: %v vs %v", err, cause)
			}
			if err := <-observed; err != cause {
				t.Fatal("fire-and-forget receipt lost fatal identity", err)
			}
			pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
				if pending.tracked != 0 {
					t.Errorf("fatal Record finally token leaked %d", pending.tracked)
				}
				return nil
			})
		})
	}
}

func TestRecordAdmissionHotBranchKeepsOriginalSuccessfulRecipe(t *testing.T) {
	owner, writer, pending, loader := recordHost(t, OSFiles{}, nil)
	branch := &admissionOwnedBranch{owner: owner, future: newWriteFuture(), value: "original-hot-branch"}
	branch.future.settle(nil)
	calls := 0
	future := startRecord(t, owner, pending, loader, []LiveRecord{{Record: plainMessage("hot-record", "original prompt")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }, Branch: func(scope *cooperative.Scope) (RecordBranchFuture, error) {
		if !scope.BelongsTo(owner) {
			return nil, cooperative.ErrRestricted
		}
		calls++
		return branch, nil
	}})
	if _, err := future.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := flushRecord(t, owner, pending).Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(recordBytes(t, writer), `"gitBranch":"original-hot-branch"`) {
		t.Fatalf("hot branch recipe changed %d/%s", calls, recordBytes(t, writer))
	}
}

func TestRecordAdmissionGitStartAfterCloseRejectsBeforeProducer(t *testing.T) {
	owner, _, pending, _ := recordHost(t, OSFiles{}, nil)
	if err := pending.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	future := &RecordFuture{owner: owner, future: newWriteFuture()}
	job := &recordContinuation{pending: pending, future: future, options: RecordOptions{Branch: func(*cooperative.Scope) (RecordBranchFuture, error) {
		calls++
		return nil, errors.New("must not create producer")
	}}}
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error { job.getBranch(s); return nil })
	if _, err := future.Wait(context.Background()); err != ErrPendingClosed || calls != 0 {
		t.Fatalf("closed Git producer admission %v/%d", err, calls)
	}
}
