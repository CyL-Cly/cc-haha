package cooperative

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// These are mechanical ownership tests, not a Bun phase oracle. Real HTTP
// checkpoint/header/body/timer composition has a separate Source-first gate.
func TestTimerPolicyWaitsForOriginalOwnerCallback(t *testing.T) {
	owner := New()
	defer owner.Close()
	finishIO := make(chan struct{})
	called := 0
	err := owner.Run(context.Background(), func(scope *Scope) error {
		ticket, err := scope.ArmTimer(10*time.Millisecond, func() { called++; close(finishIO) })
		if err != nil {
			return err
		}
		err = scope.Inline(Read, func(*Scope) error {
			<-ticket.due
			if called != 0 {
				return errors.New("clock worker ran policy inside held owner")
			}
			return nil
		})
		if err != nil {
			return err
		}
		_, err = scope.AwaitBytes(NetworkOpen, func(ctx context.Context) ([]byte, error) {
			select {
			case <-finishIO:
				return nil, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("policy callbacks=%d", called)
	}
}

func TestClearDueTicketBeforePolicyAdmission(t *testing.T) {
	owner := New()
	defer owner.Close()
	called := 0
	err := owner.Run(context.Background(), func(scope *Scope) error {
		ticket, err := scope.ArmTimer(10*time.Millisecond, func() { called++ })
		if err != nil {
			return err
		}
		err = scope.Inline(Read, func(scope *Scope) error {
			<-ticket.due
			return scope.ClearTimer(ticket)
		})
		if err != nil {
			return err
		}
		_, err = scope.AwaitBytes(NetworkOpen, func(context.Context) ([]byte, error) { return nil, nil })
		select {
		case <-ticket.done:
		default:
			return errors.New("clock worker escaped clear")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Fatalf("cleared overdue callback fired=%d", called)
	}
}

type readinessTestOperation struct {
	mu      sync.Mutex
	ready   bool
	failure error
	started chan struct{}
	joined  chan struct{}
	once    sync.Once
}

func (op *readinessTestOperation) Checkpoint() ([]byte, bool, error) {
	op.mu.Lock()
	defer op.mu.Unlock()
	if op.failure != nil {
		return nil, false, op.failure
	}
	if op.ready {
		return []byte("actual-owned-prefix"), true, nil
	}
	return nil, false, nil
}
func (op *readinessTestOperation) WaitReadable(ctx context.Context) error {
	op.once.Do(func() { close(op.started) })
	<-ctx.Done()
	close(op.joined)
	return ctx.Err()
}

func TestFreshCheckpointPrecedesDuePolicyAndJoinsUnscheduledWait(t *testing.T) {
	owner := New()
	defer owner.Close()
	op := &readinessTestOperation{started: make(chan struct{}), joined: make(chan struct{})}
	finished := make(chan error, 1)
	called := 0
	var ticket *TimerTicket
	go func() {
		finished <- owner.Run(context.Background(), func(scope *Scope) error {
			value, err := scope.AwaitNative(op)
			if err != nil {
				return err
			}
			if string(value) != "actual-owned-prefix" {
				return errors.New("checkpoint bytes lost")
			}
			return scope.ClearTimer(ticket)
		})
	}()
	<-op.started
	receipt, err := owner.Submit(context.Background(), Read, func(scope *Scope) error {
		var err error
		ticket, err = scope.ArmTimer(10*time.Millisecond, func() { called++ })
		if err != nil {
			return err
		}
		<-ticket.due
		op.mu.Lock()
		op.ready = true
		op.mu.Unlock()
		if called != 0 {
			return errors.New("policy interleaved with owner callback")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = receipt.Wait(); err != nil {
		t.Fatal(err)
	}
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Fatalf("overdue timer ran before fresh completion=%d", called)
	}
	select {
	case <-op.joined:
	default:
		t.Fatal("temporary native readiness worker not joined")
	}
}

func TestPendingCheckpointAllowsDuePolicy(t *testing.T) {
	owner := New()
	defer owner.Close()
	op := &readinessTestOperation{started: make(chan struct{}), joined: make(chan struct{})}
	original := errors.New("original resource cancellation")
	called := 0
	err := owner.Run(context.Background(), func(scope *Scope) error {
		ticket, err := scope.ArmTimer(10*time.Millisecond, func() {
			called++
			op.mu.Lock()
			op.failure = original
			op.mu.Unlock()
		})
		if err != nil {
			return err
		}
		_, err = scope.AwaitNative(op)
		if clear := scope.ClearTimer(ticket); clear != nil {
			return clear
		}
		return err
	})
	if !errors.Is(err, original) || called != 1 {
		t.Fatalf("pending result err=%v callbacks=%d", err, called)
	}
	select {
	case <-op.started:
		select {
		case <-op.joined:
		default:
			t.Fatal("pending wait worker escaped timeout")
		}
	default:
	}
}

func TestOwnerCloseJoinsUnexpiredClockTickets(t *testing.T) {
	owner := New()
	var ticket *TimerTicket
	err := owner.Run(context.Background(), func(scope *Scope) error {
		var err error
		ticket, err = scope.ArmTimer(time.Hour, func() { t.Error("unexpired policy ran") })
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	owner.Close()
	select {
	case <-ticket.done:
	default:
		t.Fatal("clock worker escaped owner close")
	}
	if len(owner.timers) != 0 {
		t.Fatal("owner retained stopped ticket")
	}
}

func TestTicketRejectsForeignOwnerAndNilCallback(t *testing.T) {
	first, second := New(), New()
	defer first.Close()
	defer second.Close()
	var ticket *TimerTicket
	if err := first.Run(context.Background(), func(scope *Scope) error {
		if _, err := scope.ArmTimer(time.Hour, nil); !errors.Is(err, ErrRestricted) {
			return errors.New("nil policy callback admitted")
		}
		var err error
		ticket, err = scope.ArmTimer(time.Hour, func() {})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := second.Run(context.Background(), func(scope *Scope) error {
		if err := scope.ClearTimer(ticket); !errors.Is(err, ErrRestricted) {
			return errors.New("foreign owner cleared timer")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestClearTimerStillRunsInCancelledOriginalFinally(t *testing.T) {
	owner := New()
	defer owner.Close()
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := owner.Run(caller, func(scope *Scope) error {
		ticket, err := scope.ArmTimer(time.Hour, func() { t.Error("cancelled request policy ran late") })
		if err != nil {
			return err
		}
		cancel()
		if err := scope.ClearTimer(ticket); err != nil {
			return errors.New("original cancelled finally could not stop policy ticket")
		}
		select {
		case <-ticket.done:
		default:
			return errors.New("cancelled finally did not join clock")
		}
		if ticket.active || len(owner.timers) != 0 {
			return errors.New("cancelled finally retained policy ticket")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type waitFaultTestOperation struct{ failure error }

func (op *waitFaultTestOperation) Checkpoint() ([]byte, bool, error)  { return nil, false, nil }
func (op *waitFaultTestOperation) WaitReadable(context.Context) error { return op.failure }

func TestOriginalNativeWaitFailureIsPreserved(t *testing.T) {
	owner := New()
	defer owner.Close()
	original := context.Canceled
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := owner.Run(ctx, func(scope *Scope) error {
		_, err := scope.AwaitNative(&waitFaultTestOperation{failure: original})
		return err
	})
	if !errors.Is(err, original) {
		t.Fatalf("native readiness error erased: %v", err)
	}
}

func TestTimerAndNativeAdmissionGuards(t *testing.T) {
	owner := New()
	defer owner.Close()
	var invalid *Scope
	if !errors.Is(invalid.ClearTimer(nil), ErrRestricted) {
		t.Fatal("nil scope admitted cleanup")
	}
	if _, err := invalid.ArmTimer(time.Hour, func() {}); !errors.Is(err, ErrRestricted) {
		t.Fatal("nil scope armed policy")
	}
	if _, err := invalid.AwaitNative(nil); !errors.Is(err, ErrRestricted) {
		t.Fatal("nil scope admitted native wait")
	}
	err := owner.Run(context.Background(), func(scope *Scope) error {
		if _, err := scope.AwaitNative(nil); !errors.Is(err, ErrRestricted) {
			return errors.New("nil operation admitted")
		}
		if !errors.Is(scope.ClearTimer(nil), ErrRestricted) {
			return errors.New("nil ticket cleared")
		}
		return scope.Inline(Read, func(scope *Scope) error {
			if _, err := scope.AwaitNative(&waitFaultTestOperation{}); !errors.Is(err, ErrRestricted) {
				return errors.New("nested callback admitted network stack")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIdleOwnerExecutesTicketAfterOriginalASKReturns(t *testing.T) {
	owner := New()
	defer owner.Close()
	fired := make(chan struct{})
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		_, err := scope.ArmTimer(10*time.Millisecond, func() { close(fired) })
		return err
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("idle owner lost clock wake")
	}
}

func TestEarlierDuePolicyCanClearLaterDueTicket(t *testing.T) {
	owner := New()
	defer owner.Close()
	var later *TimerTicket
	called := []string{}
	policyDone := make(chan struct{})
	err := owner.Run(context.Background(), func(scope *Scope) error {
		var err error
		later, err = scope.ArmTimer(20*time.Millisecond, func() { called = append(called, "later") })
		if err != nil {
			return err
		}
		earlier, err := scope.ArmTimer(10*time.Millisecond, func() {
			called = append(called, "earlier")
			if err := scope.ClearTimer(later); err != nil {
				t.Error(err)
			}
			close(policyDone)
		})
		if err != nil {
			return err
		}
		if err := scope.Inline(Read, func(*Scope) error { <-earlier.due; <-later.due; return nil }); err != nil {
			return err
		}
		_, err = scope.AwaitBytes(NetworkOpen, func(context.Context) ([]byte, error) {
			// Await's work is held until the actual owner timeout policy fires.
			<-policyDone
			return nil, nil
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(called) != 1 || called[0] != "earlier" {
		t.Fatalf("due policy order/clear=%v", called)
	}
}

type spuriousWaitTestOperation struct {
	calls   int
	failure error
}

func (op *spuriousWaitTestOperation) Checkpoint() ([]byte, bool, error) { return nil, false, nil }
func (op *spuriousWaitTestOperation) WaitReadable(context.Context) error {
	op.calls++
	if op.calls == 1 {
		return nil
	}
	return op.failure
}
func TestSpuriousNativeWakeRestartsPhysicalDemand(t *testing.T) {
	owner := New()
	defer owner.Close()
	original := errors.New("original second readiness failure")
	op := &spuriousWaitTestOperation{failure: original}
	err := owner.Run(context.Background(), func(scope *Scope) error { _, err := scope.AwaitNative(op); return err })
	if !errors.Is(err, original) || op.calls != 2 {
		t.Fatalf("spurious wait result=%v calls=%d", err, op.calls)
	}
}
