package cooperative

import (
	"context"
	"errors"
	"testing"
)

func TestNativeComputerUseCleanupKeepsAbortIndependentAndFrameRestricted(t *testing.T) {
	owner, foreign := New(), New()
	defer owner.Close()
	defer foreign.Close()
	var authority *NativeComputerUseCleanup
	var retained *Scope
	type key struct{}
	caller, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "original native owner"))
	defer cancel()
	if err := owner.Run(caller, func(scope *Scope) error {
		retained = scope
		var err error
		authority, err = NewNativeComputerUseCleanup(scope, owner)
		if err != nil {
			return err
		}
		if _, err := NewNativeComputerUseCleanup(scope, foreign); !errors.Is(err, ErrRestricted) {
			t.Error("foreign owner minted cleanup", err)
		}
		if err := scope.Inline(Read, func(read *Scope) error {
			if _, err := NewNativeComputerUseCleanup(read, owner); !errors.Is(err, ErrRestricted) {
				t.Error("ordinary callback minted cleanup", err)
			}
			return authority.Run(read, func(cleanup *Scope, _ context.Context) error {
				if err := cleanup.Frame(FrameFeed, func() error { t.Error("cleanup issued model frame"); return nil }); !errors.Is(err, ErrRestricted) {
					t.Error("ordinary callback borrowed model privilege", err)
				}
				return cleanup.AwaitFulfilled()
			})
		}); err != nil {
			return err
		}
		cancel()
		if err := scope.AwaitFulfilled(); !errors.Is(err, context.Canceled) {
			t.Error("ordinary scope cancellation guard changed", err)
		}
		return authority.Run(scope, func(cleanup *Scope, signal context.Context) error {
			if signal.Err() != nil || signal.Value(key{}) != "original native owner" || cleanup == scope {
				t.Error("cleanup inherited abort or lost context values")
			}
			if err := cleanup.Frame(FrameFinish, func() error { return nil }); !errors.Is(err, ErrRestricted) {
				t.Error("cleanup frame guard", err)
			}
			return cleanup.AwaitFulfilled()
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := authority.Run(retained, func(*Scope, context.Context) error { return nil }); !errors.Is(err, ErrRestricted) {
		t.Fatal("retained scope accepted", err)
	}
	if err := foreign.Run(context.Background(), func(scope *Scope) error {
		if err := authority.Run(scope, func(*Scope, context.Context) error { return nil }); !errors.Is(err, ErrRestricted) {
			t.Error("foreign execution accepted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read, err := owner.SubmitIndependent(context.Background(), Read, func(scope *Scope) error {
		if err := authority.Run(scope, func(*Scope, context.Context) error { t.Error("inactive owner ran cleanup"); return nil }); !errors.Is(err, ErrRestricted) {
			t.Error("inactive owner accepted", err)
		}
		return nil
	})
	if err != nil || read.Wait() != nil {
		t.Fatal("idle read", err)
	}
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		authority.BeginClose()
		if err := authority.Run(scope, func(*Scope, context.Context) error { t.Error("closed cleanup ran"); return nil }); !errors.Is(err, ErrClosed) {
			t.Error("closed authority accepted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeComputerUseCleanupGlobalCloseCancelsHeldCleanupAndJoinsOwner(t *testing.T) {
	owner := New()
	entered, released, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- owner.Run(context.Background(), func(scope *Scope) error {
			authority, err := NewNativeComputerUseCleanup(scope, owner)
			if err != nil {
				return err
			}
			return authority.Run(scope, func(_ *Scope, signal context.Context) error {
				close(entered)
				<-signal.Done()
				close(released)
				return context.Cause(signal)
			})
		})
	}()
	<-entered
	owner.Close()
	select {
	case <-released:
	default:
		t.Fatal("owner Close returned before cleanup native wait")
	}
	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Fatal("global close cause", err)
	}
}
