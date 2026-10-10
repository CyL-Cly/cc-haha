package cooperative

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"testing"
	"time"
)

// The empty OS pipe holds actual I/O. There is no clock delay or replacement
// owner in this fixture: callbacks and ASK execute on the production Owner.
func TestIdlePhysicalIOPreservesAdmittedASKAndCallbackFIFO(t *testing.T) {
	for _, waiting := range []string{"bytes", "promise"} {
		t.Run(waiting, func(t *testing.T) {
			owner := New()
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			defer reader.Close()
			defer writer.Close()
			started := make(chan struct{})
			settled := make(chan struct{})
			var order []string
			read := func(ctx context.Context) ([]byte, error) {
				stop := context.AfterFunc(ctx, func() { _ = reader.Close() })
				defer stop()
				defer close(settled)
				close(started)
				data := make([]byte, 1)
				_, err := io.ReadFull(reader, data)
				return data, err
			}
			idle, err := owner.SubmitIndependent(context.Background(), Read, func(scope *Scope) error {
				order = append(order, "idle-start")
				var data []byte
				var err error
				if waiting == "bytes" {
					data, err = scope.AwaitBytes(TranscriptOpenStat, read)
				} else {
					done := make(chan struct{})
					ctx := scope.ctx
					go func() {
						value, failure := read(ctx)
						_, post := owner.Microtask(Read, func(*Scope) error {
							data, err = value, failure
							close(done)
							return nil
						})
						if post != nil {
							close(done)
						}
					}()
					if waitErr := scope.AwaitPromise(done); waitErr != nil {
						return waitErr
					}
				}
				if err != nil {
					return err
				}
				if string(data) != "x" {
					return errors.New("physical I/O bytes changed")
				}
				order = append(order, "idle-return")
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			<-started
			// enqueue is the actual Run admission boundary. Its receipt makes
			// admission deterministic without polling or racing a test goroutine.
			ask, err := owner.enqueue(context.Background(), StartASK, func(scope *Scope) error {
				return scope.Frame(FrameFeed, func() error {
					order = append(order, "ask")
					return nil
				})
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := owner.Run(context.Background(), func(*Scope) error { t.Error("second admitted ASK nested in idle callback"); return nil }); !errors.Is(err, ErrRestricted) {
				t.Errorf("another ASK entered with an ASK already admitted: %v", err)
			}
			for _, callback := range []struct {
				class Class
				name  string
			}{{Input, "input"}, {Read, "read"}, {Cancel, "cancel"}, {Flush, "flush"}} {
				receipt, err := owner.Submit(context.Background(), callback.class, func(scope *Scope) error {
					order = append(order, callback.name)
					if err := scope.Frame(FrameFeed, func() error { return errors.New("nested ASK frame executed") }); !errors.Is(err, ErrRestricted) {
						return errors.New("callback gained ASK frame authority")
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := receipt.Wait(); err != nil {
					t.Fatal(err)
				}
			}
			timerFired := make(chan struct{})
			r, err := owner.Submit(context.Background(), Read, func(scope *Scope) error {
				_, err := scope.ArmTimer(0, func() { order = append(order, "timer"); close(timerFired) })
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Wait(); err != nil {
				t.Fatal(err)
			}
			<-timerFired
			select {
			case <-ask.Done():
				t.Errorf("admitted ASK finished before held idle I/O returned: %v", ask.Wait())
			default:
			}
			if _, err := writer.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}
			if err := idle.Wait(); err != nil {
				t.Error(err)
			}
			if err := ask.Wait(); err != nil {
				t.Error(err)
			}
			<-settled
			if err := owner.Run(context.Background(), func(scope *Scope) error {
				return scope.Frame(FrameFinish, func() error { order = append(order, "second-ask"); return nil })
			}); err != nil {
				t.Errorf("second ASK failed after actual I/O and first ASK: %v", err)
			}
			want := []string{"idle-start", "input", "read", "cancel", "flush", "timer", "idle-return", "ask", "second-ask"}
			if !reflect.DeepEqual(order, want) {
				t.Errorf("order=%v want=%v", order, want)
			}
		})
	}
}

func TestIdleHeldPhysicalIOCloseJoinsAndRejectsPendingASK(t *testing.T) {
	owner := New()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	started, cancelled, release, settled := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer owner.Close()
	idle, err := owner.SubmitIndependent(context.Background(), Read, func(scope *Scope) error {
		_, err := scope.AwaitBytes(TranscriptOpenStat, func(ctx context.Context) ([]byte, error) {
			stop := context.AfterFunc(ctx, func() { _ = reader.Close() })
			defer stop()
			defer close(settled)
			close(started)
			buffer := make([]byte, 1)
			_, err := reader.Read(buffer)
			if ctx.Err() != nil {
				close(cancelled)
				<-release // Hold the actual native I/O unwind, not a clock delay.
				return nil, context.Cause(ctx)
			}
			return buffer, err
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	ask, err := owner.enqueue(context.Background(), StartASK, func(*Scope) error {
		t.Error("queued ASK executed during Close")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { owner.Close(); close(closed) }()
	<-cancelled
	select {
	case <-owner.Done():
		t.Error("Owner.Done preceded physical I/O unwind")
	default:
	}
	select {
	case <-closed:
		t.Error("Close returned before physical I/O unwind")
	default:
	}
	close(release)
	<-closed
	<-settled
	if err := idle.Wait(); !errors.Is(err, ErrClosed) {
		t.Errorf("idle cancellation=%v", err)
	}
	if err := ask.Wait(); !errors.Is(err, ErrClosed) {
		t.Errorf("pending ASK Close result=%v", err)
	}
	if err := owner.Run(context.Background(), func(*Scope) error { t.Error("ASK after Close ran"); return nil }); !errors.Is(err, ErrClosed) {
		t.Errorf("ASK after Close=%v", err)
	}
}

func TestHeldNativeOperationKeepsActiveASKRestrictionAndTimerFIFO(t *testing.T) {
	owner := New()
	defer owner.Close()
	op := &readinessTestOperation{started: make(chan struct{}), joined: make(chan struct{})}
	done := make(chan error, 1)
	var order []string
	go func() {
		done <- owner.Run(context.Background(), func(scope *Scope) error {
			_, err := scope.AwaitNative(op)
			if err == nil {
				order = append(order, "native-return")
			}
			return err
		})
	}()
	<-op.started
	if err := owner.Run(context.Background(), func(*Scope) error { t.Error("nested second ASK ran"); return nil }); !errors.Is(err, ErrRestricted) {
		t.Errorf("active ASK admission=%v", err)
	}
	for _, class := range []Class{Input, Read, Cancel} {
		r, err := owner.Submit(context.Background(), class, func(*Scope) error { order = append(order, "callback"); return nil })
		if err != nil || r.Wait() != nil {
			t.Fatal(err)
		}
	}
	r, err := owner.Submit(context.Background(), Read, func(scope *Scope) error {
		ticket, err := scope.ArmTimer(0*time.Second, func() {
			order = append(order, "timer")
			op.mu.Lock()
			op.ready = true
			op.mu.Unlock()
		})
		if err == nil {
			<-ticket.due
		}
		return err
	})
	if err != nil || r.Wait() != nil {
		t.Fatal(err)
	}
	if err := timeout(t, done); err != nil {
		t.Fatal(err)
	}
	<-op.joined
	if !reflect.DeepEqual(order, []string{"callback", "callback", "callback", "timer", "native-return"}) {
		t.Fatal(order)
	}
	if err := owner.Run(context.Background(), func(*Scope) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
