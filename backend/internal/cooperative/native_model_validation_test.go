package cooperative

import (
	"context"
	"errors"
	"testing"
)

func TestNativeModelValidationAuthorityAndFastProofRetainCurrentOwnerBoundaries(t *testing.T) {
	owner, foreign := New(), New()
	defer owner.Close()
	defer foreign.Close()
	var authority *NativeModelValidationModels
	var stale *Scope
	var proof *NativeFastModeRequest
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		stale = scope
		var err error
		authority, err = NewNativeModelValidationModels(scope, owner)
		if err != nil {
			return err
		}
		if _, err = NewNativeModelValidationModels(scope, foreign); !errors.Is(err, ErrRestricted) {
			t.Error("foreign mint", err)
		}
		proof, err = NewNativeFastModeRequest(owner, scope, func(string, *bool) (bool, bool, error) { return false, false, nil })
		if err != nil {
			return err
		}
		if proof.Owns(foreign) {
			t.Error("foreign fast proof")
		}
		if err = scope.Frame(FrameFeed, func() error {
			if _, err := NewNativeModelValidationModels(scope, owner); !errors.Is(err, ErrRestricted) {
				t.Error("frame mint", err)
			}
			return nil
		}); err != nil {
			return err
		}
		return scope.Inline(Read, func(callback *Scope) error {
			if _, err := NewNativeModelValidationModels(callback, owner); !errors.Is(err, ErrRestricted) {
				t.Error("callback minted model authority", err)
			}
			return authority.Run(callback, context.Background(), func(producer *Scope) error {
				if producer == callback || !producer.BelongsTo(owner) {
					t.Error("not fresh original producer")
				}
				return nil
			})
		})
	}); err != nil {
		t.Fatal(err)
	}
	if proof.Current() || proof.Owns(owner) {
		t.Fatal("stale fast proof remained current")
	}
	if _, _, err := proof.Resolve("opus", nil); !errors.Is(err, ErrRestricted) {
		t.Fatal("stale fast callback", err)
	}
	if err := authority.Run(stale, context.Background(), func(*Scope) error { t.Error("stale producer entered"); return nil }); !errors.Is(err, ErrRestricted) {
		t.Fatal(err)
	}
	if err := foreign.Run(context.Background(), func(scope *Scope) error {
		if err := authority.Run(scope, context.Background(), func(*Scope) error { t.Error("foreign producer entered"); return nil }); !errors.Is(err, ErrRestricted) {
			t.Error(err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	owner.Close()
	if proof.Current() {
		t.Fatal("closed owner fast proof")
	}
}

func TestNativeFastSideQuestionProofBelongsOnlyToExecutingSegment(t *testing.T) {
	owner := New()
	defer owner.Close()
	var proof *NativeFastModeRequest
	var parked *Scope
	release, done := make(chan struct{}), make(chan error, 1)
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		authority, err := NewNativeSideQuestionModels(scope, owner)
		if err != nil {
			return err
		}
		return authority.Start(scope, context.Background(), func(child *Scope) error {
			parked = child
			proof, err = NewNativeFastModeRequest(owner, child, func(string, *bool) (bool, bool, error) { return true, true, nil })
			if err != nil || !proof.Current() {
				return errors.New("actual child did not mint current fast proof")
			}
			_, err = child.AwaitBytes(RetryTimer, func(context.Context) ([]byte, error) { <-release; return nil, nil })
			if err != nil {
				return err
			}
			if !proof.Current() {
				return errors.New("resumed child lost actual fast proof")
			}
			return nil
		}, func(_ *Scope, failure error) { done <- failure })
	}); err != nil {
		t.Fatal(err)
	}
	receipt, err := owner.SubmitIndependent(context.Background(), Read, func(*Scope) error {
		if proof.Current() {
			t.Error("parked child fast proof remained current")
		}
		if _, _, err := proof.Resolve("opus", nil); !errors.Is(err, ErrRestricted) {
			t.Error("parked resolver", err)
		}
		if _, err := NewNativeFastModeRequest(owner, parked, func(string, *bool) (bool, bool, error) { return true, true, nil }); !errors.Is(err, ErrRestricted) {
			t.Error("parked proof mint", err)
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
	receipt, err = owner.SubmitIndependent(context.Background(), Read, func(*Scope) error {
		if proof.Current() {
			t.Error("finished child proof remained current")
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
}
