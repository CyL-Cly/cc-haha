package cooperative

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeSideQuestionSealPreservesCanonicalAndGenericGuards(t *testing.T) {
	owner, foreign := New(), New()
	defer owner.Close()
	defer foreign.Close()
	var proof *NativeSideQuestionModels
	var stale *Scope
	if e := owner.Run(context.Background(), func(scope *Scope) error {
		stale = scope
		if _, e := NewNativeSideQuestionModels(scope, foreign); !errors.Is(e, ErrRestricted) {
			return errors.New("foreign Owner minted side authority")
		}
		var e error
		proof, e = NewNativeSideQuestionModels(scope, owner)
		if e != nil {
			return e
		}
		return scope.Inline(Read, func(callback *Scope) error {
			if _, e := NewNativeSideQuestionModels(callback, owner); !errors.Is(e, ErrRestricted) {
				return errors.New("Read callback minted side authority")
			}
			return nil
		})
	}); e != nil {
		t.Fatal(e)
	}
	if e := proof.Start(stale, context.Background(), func(*Scope) error { return nil }, func(*Scope, error) {}); !errors.Is(e, ErrRestricted) {
		t.Fatal("stale scope", e)
	}
	if e := foreign.Run(context.Background(), func(scope *Scope) error {
		if e := proof.Start(scope, context.Background(), func(*Scope) error { return nil }, func(*Scope, error) {}); !errors.Is(e, ErrRestricted) {
			return errors.New("foreign side stack ran")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	receipt, e := owner.SubmitIndependent(context.Background(), Read, func(scope *Scope) error {
		if e := scope.Frame(FrameFeed, func() error { return errors.New("generic callback frame entered") }); !errors.Is(e, ErrRestricted) {
			return errors.New("generic Read acquired model frame")
		}
		if _, e := scope.AwaitBytes(NetworkOpen, func(context.Context) ([]byte, error) { return nil, nil }); !errors.Is(e, ErrRestricted) {
			return errors.New("generic Read acquired network producer")
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if e := proof.Start(scope, canceled, func(*Scope) error { return errors.New("preaborted side ran") }, func(*Scope, error) {}); !errors.Is(e, context.Canceled) {
			return errors.New("preabort mismatch")
		}
		complete := false
		if e := proof.Start(scope, context.Background(), func(child *Scope) error {
			if !proof.Current(child) || !child.BelongsTo(owner) {
				return errors.New("side scope lost actual issuer")
			}
			if e := child.Frame(FrameFeed, func() error { return nil }); e != nil {
				return e
			}
			return child.Inline(Read, func(callback *Scope) error {
				if e := callback.Frame(FrameFeed, func() error { return nil }); !errors.Is(e, ErrRestricted) {
					return errors.New("nested generic callback acquired side frame")
				}
				return nil
			})
		}, func(_ *Scope, e error) {
			if e != nil {
				t.Error(e)
			}
			complete = true
		}); e != nil {
			return e
		}
		if !complete {
			return errors.New("no-await side changed immediate completion")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = receipt.Wait(); e != nil {
		t.Fatal(e)
	}
}

type heldSideNativeOperation struct {
	entered chan struct{}
	joined  *atomic.Bool
}

func (op *heldSideNativeOperation) Checkpoint() ([]byte, bool, error) { return nil, false, nil }
func (op *heldSideNativeOperation) WaitReadable(ctx context.Context) error {
	close(op.entered)
	<-ctx.Done()
	op.joined.Store(true)
	return context.Cause(ctx)
}
func TestNativeSideQuestionOwnerCloseJoinsSuspendedNativeAndFinally(t *testing.T) {
	owner := New()
	var proof *NativeSideQuestionModels
	if e := owner.Run(context.Background(), func(scope *Scope) error { var e error; proof, e = NewNativeSideQuestionModels(scope, owner); return e }); e != nil {
		t.Fatal(e)
	}
	entered, finished := make(chan struct{}), make(chan struct{})
	var joined atomic.Bool
	receipt, e := owner.SubmitIndependent(context.Background(), Read, func(scope *Scope) error {
		return proof.Start(scope, context.Background(), func(child *Scope) error {
			defer close(finished)
			_, e := child.AwaitNative(&heldSideNativeOperation{entered, &joined})
			return e
		}, func(_ *Scope, e error) {
			if !errors.Is(e, ErrClosed) {
				t.Error("administrative side error", e)
			}
		})
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = receipt.Wait(); e != nil {
		t.Fatal(e)
	}
	<-entered
	closed := make(chan struct{})
	go func() { owner.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Owner failed to drain its sealed side stack")
	}
	if !joined.Load() {
		t.Fatal("native readiness waiter escaped Owner Close")
	}
	select {
	case <-finished:
	default:
		t.Fatal("side finally escaped Owner Close")
	}
}

func TestNativeSideQuestionJoinOwnsQueuedStopHookAfterRootReturns(t *testing.T) {
	owner := New()
	defer owner.Close()
	var proof *NativeSideQuestionModels
	var prompt *NativePromptHookModels
	if e := owner.Run(context.Background(), func(scope *Scope) error {
		var e error
		proof, e = NewNativeSideQuestionModels(scope, owner)
		if e == nil {
			prompt, e = NewNativePromptHookModels(scope, owner)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var parked *Scope
	receipt, e := owner.SubmitIndependent(context.Background(), Read, func(scope *Scope) error {
		if _, handled, e := scope.SubmitNativeSideQuestionStopHook(context.Background(), owner, "SubagentStop", func(*Scope) error { return errors.New("generic callback unexpectedly acquired child job") }); e != nil || handled {
			return errors.New("generic callback escaped")
		}
		return proof.Start(scope, context.Background(), func(root *Scope) error {
			_, handled, e := root.SubmitNativeSideQuestionStopHook(context.Background(), owner, "SubagentStop", func(child *Scope) error {
				return prompt.Run(child, context.Background(), func(model *Scope) error {
					parked = model
					_, e := model.AwaitBytes(StreamNext, func(context.Context) ([]byte, error) { close(entered); <-release; return nil, nil })
					return e
				})
			})
			if !handled {
				return errors.New("actual side job not admitted")
			}
			return e
		}, func(*Scope, error) {})
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = receipt.Wait(); e != nil {
		t.Fatal(e)
	}
	<-entered
	observer, e := owner.SubmitIndependent(context.Background(), Read, func(*Scope) error {
		if parked.IsNativeSideQuestion(owner) {
			return errors.New("parked stack retained current-segment proof")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = observer.Wait(); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e := proof.Join(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("caller deadline released queued descendant", e)
	}
	go func() { _ = proof.Join(context.Background()); close(finished) }()
	select {
	case <-finished:
		t.Fatal("side child escaped group Join")
	default:
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("child physical completion failed to settle group Join")
	}
}

func TestNativeSideQuestionParkedScopeCannotUseAuxiliaryAuthority(t *testing.T) {
	for _, mainActive := range []bool{false, true} {
		t.Run(fmt.Sprint("main-active-", mainActive), func(t *testing.T) {
			owner := New()
			defer owner.Close()
			var proof *NativeSideQuestionModels
			var prompt *NativePromptHookModels
			if e := owner.Run(context.Background(), func(scope *Scope) error {
				var e error
				proof, e = NewNativeSideQuestionModels(scope, owner)
				if e == nil {
					prompt, e = NewNativePromptHookModels(scope, owner)
				}
				return e
			}); e != nil {
				t.Fatal(e)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			var parked *Scope
			receipt, e := owner.SubmitIndependent(context.Background(), Read, func(scope *Scope) error {
				return proof.Start(scope, context.Background(), func(child *Scope) error {
					parked = child
					_, e := child.AwaitBytes(StreamNext, func(context.Context) ([]byte, error) { close(entered); <-release; return nil, nil })
					return e
				}, func(*Scope, error) {})
			})
			if e != nil {
				t.Fatal(e)
			}
			if e = receipt.Wait(); e != nil {
				t.Fatal(e)
			}
			<-entered
			check := func(*Scope) error {
				enteredProducer := false
				failure := prompt.Run(parked, context.Background(), func(*Scope) error { enteredProducer = true; return nil })
				if !errors.Is(failure, ErrRestricted) || enteredProducer {
					return fmt.Errorf("parked side scope entered auxiliary model: error=%v producer=%t", failure, enteredProducer)
				}
				return nil
			}
			if mainActive {
				e = owner.Run(context.Background(), check)
			} else {
				receipt, e = owner.SubmitIndependent(context.Background(), Read, check)
				if e == nil {
					e = receipt.Wait()
				}
			}
			if e != nil {
				t.Error(e)
			}
			releaseOnce.Do(func() { close(release) })
			if e := proof.Join(context.Background()); e != nil {
				t.Fatal(e)
			}
		})
	}
}
