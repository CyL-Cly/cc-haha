package cooperative

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// Composition contracts exercise the canonical primitive, not a Bun phase oracle.
func TestCompositionPromiseWaitAdmitsTimerWithoutStarvingContinuation(t *testing.T) {
	owner := New()
	defer owner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var order []string
	err := owner.Run(ctx, func(scope *Scope) error {
		done := make(chan struct{})
		ticket, err := scope.ArmTimer(time.Millisecond, func() { order = append(order, "timer"); close(done) })
		if err != nil {
			return err
		}
		defer scope.ClearTimer(ticket)
		if err = scope.AwaitPromise(done); err != nil {
			return err
		}
		order = append(order, "resumed")
		return nil
	})
	if err != nil || !reflect.DeepEqual(order, []string{"timer", "resumed"}) {
		t.Fatalf("parked promise starved timer: err=%v order=%v", err, order)
	}
}

func TestCompositionFulfilledReactionPrecedesOverdueTimer(t *testing.T) {
	owner := New()
	defer owner.Close()
	err := owner.Run(context.Background(), func(scope *Scope) error {
		fired := false
		ticket, err := scope.ArmTimer(time.Millisecond, func() { fired = true })
		if err != nil {
			return err
		}
		defer scope.ClearTimer(ticket)
		if err = scope.Inline(Read, func(*Scope) error { <-ticket.due; return nil }); err != nil {
			return err
		}
		if err = scope.AwaitFulfilled(); err != nil {
			return err
		}
		if fired {
			return errors.New("overdue timer overtook own fulfilled FIFO reaction")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCompositionCancellationKeepsOnlyExplicitIndependentCompletionAlive(t *testing.T) {
	owner := New()
	defer owner.Close()
	cause := errors.New("original model abort")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ordinaryRan, independentRan := false, false
	var ordinary, independent *Receipt
	err := owner.Run(ctx, func(scope *Scope) error {
		_, err := owner.Submit(context.Background(), Cancel, func(*Scope) error { cancel(cause); return nil })
		if err != nil {
			return err
		}
		ordinary, err = owner.Submit(context.Background(), Read, func(*Scope) error { ordinaryRan = true; return nil })
		if err != nil {
			return err
		}
		independent, err = owner.SubmitIndependent(context.Background(), Flush, func(fresh *Scope) error {
			if err := fresh.check(); err != nil {
				return err
			}
			independentRan = true
			return nil
		})
		if err != nil {
			return err
		}
		_, err = scope.AwaitBytes(TranscriptAppend, func(worker context.Context) ([]byte, error) { <-worker.Done(); return nil, context.Cause(worker) })
		return err
	})
	if !errors.Is(err, cause) || !errors.Is(ordinary.Wait(), cause) || independent.Wait() != nil || ordinaryRan || !independentRan {
		t.Fatalf("lifetime leaked: err=%v ordinary=%v independent=%v", err, ordinaryRan, independentRan)
	}
}

func TestCompositionDoneFollowsTimerJoin(t *testing.T) {
	owner := New()
	var ticket *TimerTicket
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		var err error
		ticket, err = scope.ArmTimer(time.Hour, func() { t.Error("administratively closed timer fired") })
		return err
	}); err != nil {
		t.Fatal(err)
	}
	owner.Close()
	select {
	case <-owner.Done():
	default:
		t.Fatal("Done not closed")
	}
	select {
	case <-ticket.done:
	default:
		t.Fatal("Done preceded timer worker join")
	}
}
