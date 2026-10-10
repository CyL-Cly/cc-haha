package cooperative

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// This is a mechanical timer/continuation contract. The original SDK HTTPS
// oracle, not a timer receipt or these expected literals, proves disposition.
func TestRejectedFetchAdmitsDuePolicyBeforeItsOriginalFinally(t *testing.T) {
	owner := New()
	defer owner.Close()
	failure := errors.New("original native creation failure")
	var phases []string
	err := owner.Run(context.Background(), func(scope *Scope) error {
		ticket, err := scope.ArmTimer(time.Millisecond, func() { phases = append(phases, "timer") })
		if err != nil {
			return err
		}
		<-ticket.due // Actual clock notification, never sleep-based padding.
		if err := scope.RejectFetch(failure); err != failure {
			return errors.New("native rejection identity was replaced")
		}
		phases = append(phases, "rejected")
		if err := scope.ClearTimer(ticket); err != nil {
			return err
		}
		phases = append(phases, "clear")
		select {
		case <-ticket.done:
		default:
			return errors.New("rejected fetch finally did not join clock")
		}
		return nil
	})
	if err != nil || !reflect.DeepEqual(phases, []string{"timer", "rejected", "clear"}) {
		t.Fatalf("rejection phases=%v err=%v", phases, err)
	}
}

func TestRejectedFetchDoesNotInventExpiryBeforeDeadline(t *testing.T) {
	owner := New()
	defer owner.Close()
	called := 0
	failure := errors.New("original native rejection before deadline")
	err := owner.Run(context.Background(), func(scope *Scope) error {
		ticket, err := scope.ArmTimer(time.Hour, func() { called++ })
		if err != nil {
			return err
		}
		if scope.RejectFetch(failure) != failure || called != 0 {
			return errors.New("unexpired native rejection became a fabricated timeout")
		}
		return scope.ClearTimer(ticket)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRejectedFetchGuardsKeepOwnerAndCleanupAlive(t *testing.T) {
	owner := New()
	defer owner.Close()
	var absent, stale *Scope
	if !errors.Is(absent.RejectFetch(errors.New("not admitted")), ErrRestricted) {
		t.Fatal("nil rejection continuation admitted")
	}
	err := owner.Run(context.Background(), func(scope *Scope) error {
		stale = scope
		if !errors.Is(scope.RejectFetch(nil), ErrRestricted) {
			return errors.New("missing rejection admitted")
		}
		if err := scope.Frame(FrameFeed, func() error {
			if !errors.Is(scope.RejectFetch(errors.New("nested frame")), ErrRestricted) {
				return errors.New("frame admitted another fetch continuation")
			}
			return nil
		}); err != nil {
			return err
		}
		return scope.Inline(Read, func(scope *Scope) error {
			if !errors.Is(scope.RejectFetch(errors.New("nested callback")), ErrRestricted) {
				return errors.New("callback admitted another fetch continuation")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(stale.RejectFetch(errors.New("expired")), ErrRestricted) {
		t.Fatal("retained stale continuation admitted")
	}
}

func TestCallerCancellationPrecedesRejectedFetchDuePolicy(t *testing.T) {
	owner := New()
	defer owner.Close()
	caller, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	cause := errors.New("original caller cancellation")
	called := 0
	err := owner.Run(caller, func(scope *Scope) error {
		ticket, err := scope.ArmTimer(time.Millisecond, func() { called++ })
		if err != nil {
			return err
		}
		<-ticket.due
		cancel(cause)
		if scope.RejectFetch(errors.New("native creation failure")) != cause || called != 0 {
			return errors.New("caller cancellation lost priority to timer or native fault")
		}
		return scope.ClearTimer(ticket)
	})
	if err != nil {
		t.Fatal(err)
	}
}
