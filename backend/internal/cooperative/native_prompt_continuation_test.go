package cooperative

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestNativePromptContinuationStartsSiblingsAtActualAwaitAndRejectsModelAuthority(t *testing.T) {
	owner := New()
	defer owner.Close()
	foreign := New()
	defer foreign.Close()
	var lanes *NativePromptContinuations
	var retained *Scope
	var side *NativeSideQuestionModels
	var hooks *NativePermissionRequestHooks
	var events []string
	entered, release, completed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	err := owner.Run(context.Background(), func(scope *Scope) error {
		var err error
		lanes, err = NewNativePromptContinuations(scope, owner)
		if err != nil {
			return err
		}
		side, err = NewNativeSideQuestionModels(scope, owner)
		if err != nil {
			return err
		}
		hooks, err = NewNativePermissionRequestHooks(scope, owner)
		if err != nil {
			return err
		}
		if err := scope.Inline(Read, func(callback *Scope) error {
			if err := lanes.Start(callback, context.Background(), func(*Scope) error { t.Error("generic callback started prompt"); return nil }, func(*Scope, error) {}); !errors.Is(err, ErrRestricted) {
				t.Errorf("callback Start: %v", err)
			}
			return nil
		}); err != nil {
			return err
		}
		if err := lanes.Start(scope, context.Background(), func(part *Scope) error {
			retained = part
			events = append(events, "first prefix")
			if !part.IsNativePromptContinuation(owner) || part.IsNativeSideQuestion(owner) || part.IsNativeAutoDream(owner) {
				t.Error("prompt identity crossed domain")
			}
			if err := part.Frame(FrameFeed, func() error { t.Error("prompt opened model"); return nil }); !errors.Is(err, ErrRestricted) {
				t.Errorf("Frame: %v", err)
			}
			if _, err := NewNativeAuxiliaryModels(part, owner); !errors.Is(err, ErrRestricted) {
				t.Errorf("auxiliary mint: %v", err)
			}
			if err := side.Start(part, context.Background(), func(*Scope) error { t.Error("prompt borrowed side authority"); return nil }, func(*Scope, error) {}); !errors.Is(err, ErrRestricted) {
				t.Errorf("side Start: %v", err)
			}
			if err := hooks.Start(part, context.Background(), func(*Scope, context.Context) error { t.Error("prompt borrowed permission authority"); return nil }, func(*Scope, error) {}); !errors.Is(err, ErrRestricted) {
				t.Errorf("permission Start: %v", err)
			}
			close(entered)
			if err := part.AwaitPromise(release); err != nil {
				return err
			}
			events = append(events, "first completion")
			return nil
		}, func(*Scope, error) { close(completed) }); err != nil {
			return err
		}
		select {
		case <-entered:
		default:
			t.Error("Start returned before synchronous prefix")
		}
		if retained.IsNativePromptContinuation(owner) {
			t.Error("parked Scope kept current token")
		}
		if err := lanes.Start(retained, context.Background(), func(*Scope) error { t.Error("retained caller started prompt"); return nil }, func(*Scope, error) {}); !errors.Is(err, ErrRestricted) {
			t.Errorf("retained Start: %v", err)
		}
		if err := lanes.Start(scope, context.Background(), func(*Scope) error { events = append(events, "second prefix"); return nil }, func(*Scope, error) {}); err != nil {
			return err
		}
		if !reflect.DeepEqual(events, []string{"first prefix", "second prefix"}) {
			t.Errorf("prefix order: %v", events)
		}
		close(release)
		return scope.AwaitPromise(completed)
	})
	if err != nil {
		t.Fatal(err)
	}
	if retained.BelongsTo(owner) {
		t.Fatal("completed Scope remained valid")
	}
	if err := foreign.Run(context.Background(), func(scope *Scope) error {
		if err := lanes.Start(scope, context.Background(), func(*Scope) error { t.Error("foreign prompt ran"); return nil }, func(*Scope, error) {}); !errors.Is(err, ErrRestricted) {
			t.Errorf("foreign Start: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lanes.BeginClose()
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		if err := lanes.Start(scope, context.Background(), func(*Scope) error { t.Error("closed prompt ran"); return nil }, func(*Scope, error) {}); !errors.Is(err, ErrClosed) {
			t.Errorf("closed Start: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := lanes.Join(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNativePromptContinuationOwnerCloseJoinsParkedStack(t *testing.T) {
	owner := New()
	var lanes *NativePromptContinuations
	complete := make(chan error, 1)
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		var err error
		lanes, err = NewNativePromptContinuations(scope, owner)
		if err != nil {
			return err
		}
		return lanes.Start(scope, context.Background(), func(part *Scope) error { return part.AwaitPromise(make(chan struct{})) }, func(_ *Scope, failure error) { complete <- failure })
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { owner.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Owner close did not join prompt stack")
	}
	if failure := <-complete; failure == nil {
		t.Fatal("closing parked prompt succeeded")
	}
	if err := lanes.Join(context.Background()); err != nil {
		t.Fatal(err)
	}
}
