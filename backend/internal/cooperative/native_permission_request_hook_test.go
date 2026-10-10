package cooperative

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestPermissionHookAuthoritySharesExclusiveOwnerTokenWithoutSideIdentityAndJoinsLateDescendants(t *testing.T) {
	owner := New()
	defer owner.Close()
	foreign := New()
	defer foreign.Close()
	wait := make(chan struct{})
	var authority *NativePermissionRequestHooks
	var retained *Scope
	var completed atomic.Bool
	err := owner.Run(context.Background(), func(scope *Scope) error {
		var err error
		authority, err = NewNativePermissionRequestHooks(scope, owner)
		if err != nil {
			return err
		}
		if _, err = NewNativePermissionRequestHooks(scope, foreign); !errors.Is(err, ErrRestricted) {
			t.Fatal("foreign issuer", err)
		}
		return scope.Inline(Read, func(read *Scope) error {
			if _, err := NewNativePermissionRequestHooks(read, owner); !errors.Is(err, ErrRestricted) {
				t.Fatal("Read minted hook authority", err)
			}
			return authority.Start(read, context.Background(), func(next *Scope, _ context.Context) error {
				retained = next
				if !authority.Current(next) || next.IsNativeSideQuestion(owner) {
					t.Fatal("permission lane claimed other identity")
				}
				_, handled, err := next.SubmitNativeSideQuestionStopHook(context.Background(), owner, "SubagentStop", func(*Scope) error { t.Error("permission ran side job"); return nil })
				if handled || err != nil {
					t.Fatal("permission borrowed side authority", handled, err)
				}
				_, handled, err = next.SubmitNativePermissionRequestHook(owner, "PermissionRequest", func(child *Scope) error {
					if !authority.Current(child) || child.IsNativeSideQuestion(owner) {
						t.Error("descendant lost permission identity")
					}
					return child.AwaitPromise(wait)
				})
				if !handled || err != nil {
					return err
				}
				return nil
			}, func(*Scope, error) { completed.Store(true) })
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !completed.Load() {
		t.Fatal("root source callback did not finish")
	}
	deadline, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err = authority.Join(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("Join missed pre-registered held descendant", err)
	}
	receipt, err := owner.SubmitIndependent(context.Background(), Read, func(*Scope) error { close(wait); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = receipt.Wait(); err != nil {
		t.Fatal(err)
	}
	if err = authority.Join(context.Background()); err != nil {
		t.Fatal(err)
	}
	if authority.Current(retained) {
		t.Fatal("finished root kept current authority")
	}
}

func TestPermissionHookAdministrativeCloseCancelsAndJoinsActualPhysicalWait(t *testing.T) {
	owner := New()
	entered := make(chan struct{})
	physicalDone := make(chan struct{})
	rootDone := make(chan error, 1)
	err := owner.Run(context.Background(), func(scope *Scope) error {
		proof, err := NewNativePermissionRequestHooks(scope, owner)
		if err != nil {
			return err
		}
		return proof.Start(scope, context.Background(), func(producer *Scope, _ context.Context) error {
			_, err := producer.AwaitBytes(TranscriptOpenStat, func(ctx context.Context) ([]byte, error) {
				close(entered)
				<-ctx.Done()
				close(physicalDone)
				return nil, context.Cause(ctx)
			})
			return err
		}, func(_ *Scope, err error) { rootDone <- err })
	})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	owner.Close()
	select {
	case <-physicalDone:
	default:
		t.Fatal("Owner.Close released physical hook wait")
	}
	if err = <-rootDone; !errors.Is(err, ErrClosed) {
		t.Fatal("hook Close cause", err)
	}
}

func TestPermissionHookParkedScopeCannotBorrowPromptAuthorityWithActiveOrIdleMainOwner(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "active"}[active], func(t *testing.T) {
			owner := New()
			defer owner.Close()
			wait := make(chan struct{})
			var hooks *NativePermissionRequestHooks
			var prompt *NativePromptHookModels
			var parked *Scope
			err := owner.Run(context.Background(), func(scope *Scope) error {
				var err error
				hooks, err = NewNativePermissionRequestHooks(scope, owner)
				if err != nil {
					return err
				}
				prompt, err = NewNativePromptHookModels(scope, owner)
				if err != nil {
					return err
				}
				return hooks.Start(scope, context.Background(), func(scope *Scope, _ context.Context) error { parked = scope; return scope.AwaitPromise(wait) }, func(*Scope, error) {})
			})
			if err != nil {
				t.Fatal(err)
			}
			check := func(scope *Scope) error {
				if hooks.Current(parked) || parked.IsNativeSideQuestion(owner) {
					t.Error("parked permission claimed a running token")
				}
				entered := false
				err := prompt.Run(parked, context.Background(), func(*Scope) error { entered = true; return nil })
				if entered || !errors.Is(err, ErrRestricted) {
					t.Error("parked scope borrowed prompt stack", entered, err)
				}
				close(wait)
				return nil
			}
			if active {
				err = owner.Run(context.Background(), check)
			} else {
				receipt, failure := owner.SubmitIndependent(context.Background(), Read, check)
				err = failure
				if err == nil {
					err = receipt.Wait()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = hooks.Join(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
