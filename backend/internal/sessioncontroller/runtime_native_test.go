package sessioncontroller

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

func TestRuntimeNativeReferenceIncludesOwnerCompletionAndContinuation(t *testing.T) {
	owner := cooperative.New()

	pending := &PendingWriter{owner: owner}
	firstR, firstW, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer firstR.Close()
	defer firstW.Close()
	secondR, secondW, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer secondR.Close()
	defer secondW.Close()
	nativeReturned := make(chan struct{})
	ownerHeld := make(chan struct{})
	releaseOwner := make(chan struct{})
	var releaseOnce sync.Once
	defer func() { releaseOnce.Do(func() { close(releaseOwner) }); owner.Close() }()
	secondStarted := make(chan struct{})
	secondDelivered := make(chan struct{})
	initialDone := make(chan error, 1)
	future := newWriteFuture()
	go func() {
		initialDone <- owner.Run(context.Background(), func(s *cooperative.Scope) error {
			dispatchRecord(pending, future, func() error { var b [1]byte; _, e := io.ReadFull(firstR, b[:]); close(nativeReturned); return e }, func(s *cooperative.Scope, e error) {
				if e != nil {
					t.Error(e)
					return
				}
				dispatchRecord(pending, future, func() error { close(secondStarted); var b [1]byte; _, e := io.ReadFull(secondR, b[:]); return e }, func(*cooperative.Scope, error) { close(secondDelivered) })
			})
			close(ownerHeld)
			<-releaseOwner
			return nil
		})
	}()
	<-ownerHeld
	if _, e := firstW.Write([]byte("a")); e != nil {
		t.Fatal(e)
	}
	<-nativeReturned
	count, _ := pending.runtimeNative.snapshot()
	if count != 1 {
		t.Fatalf("worker result released owner-delivery ref: %d", count)
	}
	ended := make(chan error, 1)
	go func() { ended <- pending.WaitRuntimeNative(context.Background()) }()
	releaseOnce.Do(func() { close(releaseOwner) })
	if e := <-initialDone; e != nil {
		t.Fatal(e)
	}
	<-secondStarted
	// The second actual native read is still held. No ready raw syscall result
	// or pure reaction can turn the process checkpoint into an early return.
	select {
	case e := <-ended:
		t.Fatal("native continuation lost reference", e)
	default:
	}
	if _, e := secondW.Write([]byte("b")); e != nil {
		t.Fatal(e)
	}
	if e := <-ended; e != nil {
		t.Fatal(e)
	}
	select {
	case <-secondDelivered:
	default:
		t.Fatal("runtime checkpoint preceded owner delivery")
	}
	if count, _ := pending.runtimeNative.snapshot(); count != 0 {
		t.Fatal(count)
	}
}

func TestRuntimeCheckpointDoesNotJoinPureUnresolvedRecord(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	pending := &PendingWriter{owner: owner}
	future := &RecordFuture{owner: owner, future: newWriteFuture()}
	called := false
	if e := owner.Run(context.Background(), func(s *cooperative.Scope) error {
		return future.ObserveDetached(s, func(*cooperative.Scope, error) { called = true })
	}); e != nil {
		t.Fatal(e)
	}
	if e := pending.WaitRuntimeNative(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case <-future.Done():
		t.Fatal("pure unresolved Record was settled")
	default:
	}
	if called {
		t.Fatal("process checkpoint fabricated Record completion")
	}
}

func TestRuntimeNativeCheckpointCancellationAndOwnerClose(t *testing.T) {
	if (&PendingWriter{}).WaitRuntimeNative(context.Background()) != cooperative.ErrRestricted {
		t.Fatal("nil owner accepted")
	}
	if (*PendingWriter)(nil).WaitRuntimeNative(context.Background()) != cooperative.ErrRestricted {
		t.Fatal("nil pending accepted")
	}
	owner := cooperative.New()
	pending := &PendingWriter{owner: owner}
	if pending.WaitRuntimeNative(nil) != cooperative.ErrRestricted {
		t.Fatal("nil context accepted")
	}
	cancelled, stop := context.WithCancelCause(context.Background())
	cause := errors.New("runtime administration only")
	stop(cause)
	if e := pending.WaitRuntimeNative(cancelled); e != cause {
		t.Fatal(e)
	}
	held, stop := context.WithCancelCause(context.Background())
	pending.runtimeNative.begin()
	completed := make(chan error, 1)
	go func() { completed <- pending.WaitRuntimeNative(held) }()
	stop(cause)
	if e := <-completed; e != cause {
		t.Fatal(e)
	}
	finished := make(chan error, 1)
	go func() { finished <- pending.WaitRuntimeNative(context.Background()) }()
	owner.Close()
	select {
	case e := <-finished:
		if e != cooperative.ErrClosed {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner close did not end native runtime observer")
	}
	pending.runtimeNative.end()
}
