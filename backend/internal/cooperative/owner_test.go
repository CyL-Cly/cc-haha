package cooperative

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func timeout(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("cooperative owner deadlocked")
		return nil
	}
}
func TestAwaitWorkerRPCAndRestrictedOwnerCallbacks(t *testing.T) {
	owner := New()
	defer owner.Close()
	ctx := context.Background()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	values := []string{}
	home := t.TempDir()
	go func() {
		done <- owner.Run(ctx, func(scope *Scope) error {
			data, err := scope.AwaitBytes(NetworkOpen, func(context.Context) ([]byte, error) {
				close(started)
				receipt, err := owner.Submit(ctx, Read, func(on *Scope) error {
					values = append(values, "worker-RPC")
					return on.Inline(Read, func(*Scope) error { values = append(values, "inline"); return nil })
				})
				if err != nil {
					return nil, err
				}
				if err = receipt.Wait(); err != nil {
					return nil, err
				}
				<-release
				return []byte("immutable IO result"), nil
			})
			if err != nil {
				return err
			}
			values = append(values, string(data))
			return nil
		})
	}()
	<-started
	for _, class := range []Class{Input, Cancel, Flush, Read} {
		receipt, err := owner.Submit(ctx, class, func(scope *Scope) error {
			if err := scope.Frame(FrameFeed, func() error { t.Error("nested Feed ran"); return nil }); !errors.Is(err, ErrRestricted) {
				t.Error(err)
			}
			if _, err := scope.AwaitBytes(StreamNext, func(context.Context) ([]byte, error) { t.Error("nested Next ran"); return nil, nil }); !errors.Is(err, ErrRestricted) {
				t.Error(err)
			}
			if class == Flush {
				data, err := scope.AwaitBytes(TranscriptOpenStat, func(context.Context) ([]byte, error) { return []byte("owned bytes"), nil })
				if err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(home, "flush"), data, 0600)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err = receipt.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	if err := owner.Run(ctx, func(*Scope) error { t.Error("second ASK ran"); return nil }); !errors.Is(err, ErrRestricted) {
		t.Fatal(err)
	}
	for _, class := range []Class{StartASK, Next, Feed, Class(99)} {
		if _, err := owner.Submit(ctx, class, func(*Scope) error { t.Error("forbidden callback"); return nil }); !errors.Is(err, ErrRestricted) {
			t.Fatal(err)
		}
	}
	close(release)
	if err := timeout(t, done); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(values, []string{"worker-RPC", "inline", "immutable IO result"}) {
		t.Fatal(values)
	}
	data, err := os.ReadFile(filepath.Join(home, "flush"))
	if err != nil || string(data) != "owned bytes" {
		t.Fatal(err)
	}
}
func TestOriginalCancellationJoinsActualBlockedPipe(t *testing.T) {
	owner := New()
	defer owner.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	started := make(chan struct{})
	settled := make(chan struct{})
	done := make(chan error, 1)
	cause := errors.New("original cancellation cause")
	go func() {
		done <- owner.Run(ctx, func(scope *Scope) error {
			_, err := scope.AwaitBytes(StreamNext, func(ioctx context.Context) ([]byte, error) {
				stop := context.AfterFunc(ioctx, func() { reader.Close() })
				defer stop()
				defer close(settled)
				close(started)
				return io.ReadAll(reader)
			})
			return err
		})
	}()
	<-started
	cancel(cause)
	if err = timeout(t, done); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	select {
	case <-settled:
	default:
		t.Fatal("IO worker escaped its retained stack")
	}
}
func TestCloseUnwindsActualYieldStackAndRejectsNewWork(t *testing.T) {
	owner := New()
	ctx := context.Background()
	entered := make(chan struct{})
	done := make(chan error, 1)
	unwound := false
	held := NewYield()
	go func() {
		done <- owner.Run(ctx, func(scope *Scope) error {
			return scope.Frame(FrameFeed, func() error { defer func() { unwound = true }(); close(entered); return scope.YieldReceipt(held) })
		})
	}()
	<-entered
	closed := make(chan struct{})
	go func() { owner.Close(); close(closed) }()
	if err := timeout(t, done); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	<-closed
	if !unwound {
		t.Fatal("frame did not unwind")
	}
	owner.Close()
	if _, err := owner.Submit(ctx, Read, func(*Scope) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := owner.Run(ctx, func(*Scope) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	held.Resolve(Resume, nil) // late consumer completion cannot resurrect a stack
}
func TestReceiptControlAndExpiredScope(t *testing.T) {
	owner := New()
	defer owner.Close()
	ctx := context.Background()
	boom := errors.New("consumer failure")
	var expired *Scope
	for _, row := range []struct {
		action        Action
		failure, want error
	}{{Resume, nil, nil}, {Stop, nil, ErrStop}, {Resume, boom, boom}, {Action(99), nil, ErrRestricted}} {
		err := owner.Run(ctx, func(scope *Scope) error {
			expired = scope
			y := NewYield()
			y.Resolve(row.action, row.failure)
			y.Resolve(Resume, nil)
			return scope.YieldReceipt(y)
		})
		if !errors.Is(err, row.want) {
			t.Fatal(err, row.want)
		}
	}
	if err := expired.Inline(Read, func(*Scope) error { return nil }); !errors.Is(err, ErrRestricted) {
		t.Fatal(err)
	}
	var missing *Scope
	if err := missing.Frame(FrameFeed, func() error { return nil }); !errors.Is(err, ErrRestricted) {
		t.Fatal(err)
	}
	if err := owner.Run(ctx, func(scope *Scope) error {
		for _, call := range []func() error{func() error { return scope.Inline(Next, func(*Scope) error { return nil }) }, func() error { return scope.Inline(Read, nil) }, func() error { return scope.Frame(FrameKind(99), func() error { return nil }) }, func() error { return scope.Frame(FrameFeed, nil) }, func() error {
			_, err := scope.AwaitBytes(WaitKind(99), func(context.Context) ([]byte, error) { return nil, nil })
			return err
		}, func() error { _, err := scope.AwaitBytes(StreamNext, nil); return err }, func() error { return scope.YieldReceipt(nil) }} {
			if err := call(); !errors.Is(err, ErrRestricted) {
				t.Error(err)
			}
		}
		return scope.Frame(FrameFinish, func() error {
			if err := scope.Frame(FrameAbort, func() error { return nil }); !errors.Is(err, ErrRestricted) {
				t.Error(err)
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
}
func TestInvalidAndPreCancelledAdmission(t *testing.T) {
	owner := New()
	defer owner.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := owner.Run(ctx, func(*Scope) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, row := range []struct {
		ctx      context.Context
		callback func(*Scope) error
	}{{nil, func(*Scope) error { return nil }}, {context.Background(), nil}} {
		if _, err := owner.Submit(row.ctx, Read, row.callback); err == nil {
			t.Fatal("invalid command accepted")
		}
	}
	receipt, err := owner.Submit(context.Background(), Read, func(scope *Scope) error {
		return scope.Frame(FrameFeed, func() error { t.Error("idle callback could start Feed"); return nil })
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = receipt.Wait(); !errors.Is(err, ErrRestricted) {
		t.Fatal(err)
	}
	select {
	case <-receipt.Done():
	default:
		t.Fatal("receipt incomplete")
	}
}
func TestOriginalCancelGuardBeforeAnotherFrameOrIO(t *testing.T) {
	owner := New()
	defer owner.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("original signal before next frame")
	if err := owner.Run(ctx, func(scope *Scope) error {
		if err := scope.Inline(Cancel, func(*Scope) error { cancel(cause); return nil }); err != nil {
			return err
		}
		for _, call := range []func() error{func() error {
			return scope.Frame(FrameAbort, func() error { t.Error("cancelled new frame executed"); return nil })
		}, func() error {
			_, err := scope.AwaitBytes(StreamNext, func(context.Context) ([]byte, error) { t.Error("cancelled I/O executed"); return nil, nil })
			return err
		}, func() error { return scope.YieldReceipt(NewYield()) }} {
			if err := call(); !errors.Is(err, cause) {
				t.Error(err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestCancellationJoinRejectsCallbacksUntilWorkerSettles(t *testing.T) {
	owner := New()
	defer owner.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("original cancellation with pending IO cleanup")
	started := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- owner.Run(ctx, func(scope *Scope) error {
			_, err := scope.AwaitBytes(TranscriptOpenStat, func(ioctx context.Context) ([]byte, error) {
				close(started)
				<-ioctx.Done()
				close(cancelled)
				<-release
				return nil, context.Cause(ioctx)
			})
			return err
		})
	}()
	<-started
	cancel(cause)
	<-cancelled
	receipt, err := owner.Submit(context.Background(), Read, func(*Scope) error { t.Error("cancelled continuation admitted mutation"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	callbackDone := make(chan error, 1)
	go func() { callbackDone <- receipt.Wait() }()
	if err = timeout(t, callbackDone); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		t.Fatal("worker was not joined", err)
	default:
	}
	close(release)
	if err = timeout(t, done); !errors.Is(err, cause) {
		t.Fatal(err)
	}
}
func TestQueuedCallbacksCompleteWhenOwnerCloses(t *testing.T) {
	owner := New()
	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- owner.Run(ctx, func(*Scope) error { close(entered); <-release; return nil }) }()
	<-entered
	receipts := []*Receipt{}
	for range 32 {
		receipt, err := owner.Submit(ctx, Read, func(*Scope) error { t.Error("closed queued callback ran"); return nil })
		if err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, receipt)
	}
	closed := make(chan struct{})
	go func() { owner.Close(); close(closed) }()
	// Observe only the real public closed admission gate; no private-state write.
	for {
		if _, err := owner.Submit(ctx, Read, func(*Scope) error { return nil }); errors.Is(err, ErrClosed) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	if err := timeout(t, done); err != nil {
		t.Fatal(err)
	}
	<-closed
	for _, receipt := range receipts {
		if err := receipt.Wait(); !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	}
}
