package cooperative

import (
	"context"
	"errors"
	"testing"
)

func TestNativeFastPermissionHookProofOnlyExecutingSegment(t *testing.T) {
	owner, foreign := New(), New()
	defer owner.Close()
	defer foreign.Close()
	var proof *NativeFastModeRequest
	var hooks *NativePermissionRequestHooks
	var parked *Scope
	release, done := make(chan struct{}), make(chan error, 1)
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		var err error
		hooks, err = NewNativePermissionRequestHooks(scope, owner)
		if err != nil {
			return err
		}
		return hooks.Start(scope, context.Background(), func(child *Scope, _ context.Context) error {
			parked = child
			proof, err = NewNativeFastModeRequest(owner, child, func(string, *bool) (bool, bool, error) { return false, true, nil })
			if err != nil {
				return err
			}
			if !proof.Current() || proof.Owns(foreign) || child.IsNativeSideQuestion(owner) {
				return errors.New("permission hook fast proof lost its actual identity")
			}
			fast, latched, err := proof.Resolve("opus-4-6", nil)
			if err != nil || fast || !latched {
				return errors.New("permission hook must retain the actual shared header latch")
			}
			_, err = child.AwaitBytes(RetryTimer, func(context.Context) ([]byte, error) { <-release; return nil, nil })
			if err != nil {
				return err
			}
			if !proof.Current() {
				return errors.New("resumed permission hook lost current fast state")
			}
			return nil
		}, func(_ *Scope, failure error) { done <- failure })
	}); err != nil {
		t.Fatal(err)
	}
	if proof == nil {
		close(release)
		t.Fatal("actual permission producer could not mint fast proof", <-done)
	}
	receipt, err := owner.SubmitIndependent(context.Background(), Read, func(*Scope) error {
		if proof.Current() {
			t.Error("parked permission proof remained current")
		}
		if _, _, err := proof.Resolve("opus", nil); !errors.Is(err, ErrRestricted) {
			t.Error("parked resolver", err)
		}
		if _, err := NewNativeFastModeRequest(owner, parked, func(string, *bool) (bool, bool, error) { return false, true, nil }); !errors.Is(err, ErrRestricted) {
			t.Error("parked permission proof mint", err)
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = hooks.Join(context.Background()); err != nil {
		t.Fatal(err)
	}
	if proof.Current() {
		t.Fatal("finished permission proof remained current")
	}
	if _, _, err = proof.Resolve("opus", nil); !errors.Is(err, ErrRestricted) {
		t.Fatal("finished resolver", err)
	}
}
