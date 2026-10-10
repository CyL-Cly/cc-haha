package cooperative

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestNativeImmediateBatchAndOwnPromiseCut(t *testing.T) {
	owner := New()
	defer owner.Close()
	var order []string
	err := owner.Run(context.Background(), func(scope *Scope) error {
		if _, err := scope.ArmImmediate(func(s *Scope) error {
			if !s.BelongsTo(owner) || s.callback != 1 {
				return errors.New("immediate callback escaped same Owner")
			}
			order = append(order, "first")
			if _, err := s.ArmImmediate(func(*Scope) error { order = append(order, "nested"); return nil }); err != nil {
				return err
			}
			_, err := owner.Microtask(Read, func(*Scope) error { order = append(order, "first-reaction"); return nil })
			return err
		}); err != nil {
			return err
		}
		if _, err := scope.ArmImmediate(func(*Scope) error {
			order = append(order, "second")
			_, err := owner.Submit(context.Background(), Read, func(*Scope) error { order = append(order, "new-ready-read"); return nil })
			return err
		}); err != nil {
			return err
		}
		if _, err := owner.Microtask(Read, func(*Scope) error { order = append(order, "already-reaction"); return nil }); err != nil {
			return err
		}
		if _, err := owner.Submit(context.Background(), Read, func(*Scope) error { order = append(order, "already-ready-read"); return nil }); err != nil {
			return err
		}
		if len(order) != 0 {
			return errors.New("registration executed callback inline")
		}
		if err := scope.AwaitImmediate(); err != nil {
			return err
		}
		order = append(order, "await-continuation")
		want := []string{"already-reaction", "already-ready-read", "first", "first-reaction", "second", "await-continuation"}
		if !reflect.DeepEqual(order, want) {
			return errors.New("current batch or own await reaction ordering changed")
		}
		if err := scope.AwaitImmediate(); err != nil {
			return err
		}
		want = append(want, "new-ready-read", "nested")
		if !reflect.DeepEqual(order, want) {
			return errors.New("new immediate joined current batch or ready read was lost")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%v: %v", err, order)
	}
}

func TestNativeImmediateDoesNotJoinUnreadyPrivatePipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	started, done := make(chan struct{}), make(chan error, 1)
	go func() { close(started); b := make([]byte, 1); _, err := io.ReadFull(r, b); done <- err }()
	<-started
	owner := New()
	defer owner.Close()
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		if err := scope.AwaitImmediate(); err != nil {
			return err
		}
		select {
		case err := <-done:
			return errors.Join(errors.New("unready pipe completed before its writer"), err)
		default:
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestNativeImmediateRunsOnParkedRealPipeProducer(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	owner := New()
	defer owner.Close()
	called := 0
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		if _, err := scope.ArmImmediate(func(s *Scope) error {
			if !s.BelongsTo(owner) {
				return errors.New("pipe writer callback escaped Owner")
			}
			called++
			_, err := w.Write([]byte("private-native-pipe"))
			return err
		}); err != nil {
			return err
		}
		value, err := scope.AwaitBytes(TranscriptOpenStat, func(context.Context) ([]byte, error) {
			b := make([]byte, len("private-native-pipe"))
			_, err := io.ReadFull(r, b)
			return b, err
		})
		if err != nil {
			return err
		}
		if string(value) != "private-native-pipe" || called != 1 {
			return errors.New("parked native producer lost immediate callback")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeImmediateRunsOnPendingCheckpointBeforeStartingWait(t *testing.T) {
	owner := New()
	defer owner.Close()
	op := &readinessTestOperation{started: make(chan struct{}), joined: make(chan struct{})}
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		if _, err := scope.ArmImmediate(func(*Scope) error {
			op.mu.Lock()
			op.ready = true
			op.mu.Unlock()
			return nil
		}); err != nil {
			return err
		}
		value, err := scope.AwaitNative(op)
		if err != nil {
			return err
		}
		if string(value) != "actual-owned-prefix" {
			return errors.New("checkpoint bytes lost")
		}
		select {
		case <-op.started:
			return errors.New("unneeded native waiter was started")
		default:
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeImmediateIdleAdmissionAndResourceRelease(t *testing.T) {
	owner := New()
	defer owner.Close()
	done := make(chan struct{})
	var ticket *ImmediateTicket
	receipt, err := owner.Submit(context.Background(), Read, func(scope *Scope) error {
		var err error
		ticket, err = scope.ArmImmediate(func(s *Scope) error {
			if !s.BelongsTo(owner) {
				return errors.New("idle callback escaped Owner")
			}
			_, err := owner.Microtask(Read, func(*Scope) error { close(done); return nil })
			return err
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.Wait(); err != nil {
		t.Fatal(err)
	}
	<-done
	if err := owner.Run(context.Background(), func(*Scope) error {
		if ticket.active || ticket.callback != nil {
			return errors.New("fired native object retained callback")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeImmediateDueTimerBetweenDistinctBatches(t *testing.T) {
	owner := New()
	defer owner.Close()
	var order []string
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		timer, err := scope.ArmTimer(time.Millisecond, func() { order = append(order, "timer") })
		if err != nil {
			return err
		}
		defer scope.ClearTimer(timer)
		// Actual clock readiness is held on owner; no sleep drives completion.
		<-timer.due
		if _, err := scope.ArmImmediate(func(s *Scope) error {
			order = append(order, "first")
			_, err := s.ArmImmediate(func(*Scope) error { order = append(order, "nested"); return nil })
			return err
		}); err != nil {
			return err
		}
		if err := scope.AwaitImmediate(); err != nil {
			return err
		}
		if !reflect.DeepEqual(order, []string{"first"}) {
			return errors.New("due timer overtook first native immediate batch")
		}
		if err := scope.AwaitImmediate(); err != nil {
			return err
		}
		if !reflect.DeepEqual(order, []string{"first", "timer", "nested"}) {
			return errors.New("recursive immediate starved already-due timer")
		}
		return nil
	}); err != nil {
		t.Fatalf("%v: %v", err, order)
	}
}

func TestNativeImmediateClearBeforeItsTurnAndForeignOwner(t *testing.T) {
	owner, foreign := New(), New()
	defer owner.Close()
	defer foreign.Close()
	called := 0
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		var later *ImmediateTicket
		if _, err := scope.ArmImmediate(func(s *Scope) error { return s.ClearImmediate(later) }); err != nil {
			return err
		}
		var err error
		later, err = scope.ArmImmediate(func(*Scope) error { called++; return nil })
		if err != nil {
			return err
		}
		if err := foreign.Run(context.Background(), func(s *Scope) error {
			if !errors.Is(s.ClearImmediate(later), ErrRestricted) {
				return errors.New("foreign Owner cleared native object")
			}
			return nil
		}); err != nil {
			return err
		}
		if err := scope.AwaitImmediate(); err != nil {
			return err
		}
		if called != 0 || later.active || later.callback != nil {
			return errors.New("cleared object invoked or retained callback")
		}
		return scope.ClearImmediate(later)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeImmediateCallerCancellationAndOriginalFinally(t *testing.T) {
	owner := New()
	defer owner.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("original immediate caller cancelled")
	called := 0
	if err := owner.Run(ctx, func(scope *Scope) error {
		ticket, err := scope.ArmImmediate(func(*Scope) error { called++; return nil })
		if err != nil {
			return err
		}
		cancel(cause)
		if !errors.Is(scope.AwaitImmediate(), cause) {
			return errors.New("cancelled caller armed or awaited new object")
		}
		if _, err := scope.ArmImmediate(func(*Scope) error { called++; return nil }); !errors.Is(err, cause) {
			return errors.New("cancelled caller admitted new immediate")
		}
		return scope.ClearImmediate(ticket)
	}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		if err := scope.AwaitImmediate(); err != nil {
			return err
		}
		if called != 0 {
			return errors.New("cancelled original callback ran late")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeImmediateOwnerCloseReleasesQueuedAndRemainingBatch(t *testing.T) {
	owner := New()
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var remaining, nested *ImmediateTicket
	called := 0
	go func() {
		finished <- owner.Run(context.Background(), func(scope *Scope) error {
			if _, err := scope.ArmImmediate(func(s *Scope) error {
				var err error
				nested, err = s.ArmImmediate(func(*Scope) error { called++; return nil })
				if err != nil {
					return err
				}
				close(started)
				<-release
				return nil
			}); err != nil {
				return err
			}
			var err error
			remaining, err = scope.ArmImmediate(func(*Scope) error { called++; return nil })
			if err != nil {
				return err
			}
			return scope.AwaitImmediate()
		})
	}()
	<-started
	closed := make(chan struct{})
	go func() { owner.Close(); close(closed) }()
	<-owner.life.Done()
	close(release)
	if err := <-finished; !errors.Is(err, ErrClosed) {
		t.Fatalf("original close cause lost: %v", err)
	}
	<-closed
	if called != 0 || remaining.active || remaining.callback != nil || nested.active || nested.callback != nil || len(owner.immediates) != 0 || len(owner.immediateBatch) != 0 {
		t.Fatal("Owner close retained/invoked native object")
	}
}

func TestNativeImmediateInvalidScopeAndNilCallbackHaveNoEffect(t *testing.T) {
	var invalid Scope
	if _, err := invalid.ArmImmediate(func(*Scope) error { t.Error("invalid callback ran"); return nil }); !errors.Is(err, ErrRestricted) {
		t.Fatal(err)
	}
	if !errors.Is(invalid.AwaitImmediate(), ErrRestricted) || !errors.Is(invalid.ClearImmediate(nil), ErrRestricted) {
		t.Fatal("invalid scope admitted native immediate")
	}
	owner := New()
	defer owner.Close()
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		if _, err := scope.ArmImmediate(nil); !errors.Is(err, ErrRestricted) {
			return errors.New("nil native callback admitted")
		}
		if !errors.Is(scope.ClearImmediate(nil), ErrRestricted) {
			return errors.New("nil native object cleared")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
