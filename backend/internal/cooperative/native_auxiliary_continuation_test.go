package cooperative

import (
	"context"
	"errors"
	"testing"
)

func TestTypedAwaitingAuxiliaryKeepsPurposeAndParkedFastProofGuards(t *testing.T) {
	owner := New()
	defer owner.Close()
	var authority *NativeAuxiliaryModels
	var retained *Scope
	var proof *NativeFastModeRequest
	if e := owner.Run(context.Background(), func(scope *Scope) error {
		var e error
		authority, e = NewNativeAuxiliaryModels(scope, owner)
		if e != nil {
			return e
		}
		return scope.Inline(Read, func(caller *Scope) error {
			if e := caller.Frame(FrameFeed, func() error { return nil }); !errors.Is(e, ErrRestricted) {
				t.Error("generic callback granted frame", e)
			}
			return authority.Run(caller, context.Background(), "web_fetch_apply", func(actual *Scope) error {
				retained = actual
				if actual.IsNativeSideQuestion(owner) || actual.IsNativeAutoDream(owner) || !actual.isNativeAwaitingAuxiliary(owner) {
					t.Error("Web continuation borrowed unrelated purpose")
				}
				proof, e = NewNativeFastModeRequest(owner, actual, func(string, *bool) (bool, bool, error) { return false, false, nil })
				if e != nil {
					return e
				}
				if !proof.Current() {
					t.Error("actual current request proof absent")
				}
				parked := make(chan struct{})
				if _, e = owner.Microtask(Read, func(callback *Scope) error {
					if proof.Current() || retained.isNativeAwaitingAuxiliary(owner) {
						t.Error("parked producer retained active purpose/fast authority")
					}
					if e := callback.Frame(FrameFeed, func() error { return nil }); !errors.Is(e, ErrRestricted) {
						t.Error("generic promise reaction gained model frame", e)
					}
					close(parked)
					return nil
				}); e != nil {
					return e
				}
				if e = actual.AwaitPromise(parked); e != nil {
					return e
				}
				if !proof.Current() {
					t.Error("resumed actual request proof lost")
				}
				return nil
			})
		})
	}); e != nil {
		t.Fatal(e)
	}
	if proof.Current() || retained.isNativeAwaitingAuxiliary(owner) {
		t.Fatal("completed producer retained request authority")
	}
	if e := authority.Run(retained, context.Background(), "web_fetch_apply", func(*Scope) error { return nil }); !errors.Is(e, ErrRestricted) {
		t.Fatal("completed producer admitted another request", e)
	}
}

func TestTypedAwaitingAuxiliaryOwnerCloseJoinsRealNativeFinally(t *testing.T) {
	owner := New()
	started, finished := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- owner.Run(context.Background(), func(scope *Scope) error {
			authority, e := NewNativeInsightsModels(scope, owner)
			if e != nil {
				return e
			}
			return authority.Run(scope, context.Background(), func(actual *Scope) error {
				defer close(finished)
				_, e := actual.AwaitBytes(NetworkOpen, func(wait context.Context) ([]byte, error) {
					close(started)
					<-wait.Done()
					return nil, context.Cause(wait)
				})
				return e
			})
		})
	}()
	<-started
	owner.Close()
	select {
	case <-finished:
	default:
		t.Fatal("Owner.Close returned before actual native finally")
	}
	if e := <-done; !errors.Is(e, context.Canceled) && !errors.Is(e, ErrClosed) {
		t.Fatal("closed auxiliary scope identity", e)
	}
}
