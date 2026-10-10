package gitbranch

import (
	"context"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The Source await of a fulfilled branch is a Promise reaction; it precedes
// an already queued ordinary I/O callback and cannot execute a provider Frame.
func TestGitResolvedReactionPrecedesAlreadyQueuedPhysicalCommand(t *testing.T) {
	h := newHarness(t, nil)
	var events []string
	err := h.owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		f := h.s.future()
		h.s.settle(scope, f, result{raw: raw("original")})
		if _, e := h.owner.Submit(context.Background(), cooperative.Read, func(*cooperative.Scope) error { events = append(events, "io"); return nil }); e != nil {
			return e
		}
		done := make(chan struct{})
		h.s.then(scope, f, func(next *cooperative.Scope, r result) {
			if !errors.Is(next.Frame(cooperative.FrameFeed, func() error { return nil }), cooperative.ErrRestricted) {
				t.Error("reaction was a provider frame")
			}
			events = append(events, "promise")
			close(done)
		})
		if e := scope.AwaitPromise(done); e != nil {
			return e
		}
		if fmt.Sprint(events) != "[promise]" {
			return fmt.Errorf("Source Promise resumed before physical IO: %v", events)
		}
		return nil
	})
	h.call(t, func(*cooperative.Scope) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(events) != "[promise io]" {
		t.Fatal(events)
	}
}

func TestBranchThenOwnerFIFOHotColdOriginalErrorAndGuards(t *testing.T) {
	h := newHarness(t, nil)
	var events []string
	var stale *cooperative.Scope
	var future *BranchFuture
	h.call(t, func(scope *cooperative.Scope) error {
		stale = scope
		f := h.s.future()
		future = &BranchFuture{f}
		run := func(label string) func(*cooperative.Scope, string, error) {
			return func(next *cooperative.Scope, value string, err error) {
				if err != nil || value != "original" || !next.BelongsTo(h.owner) {
					t.Errorf("original branch reaction %q %v", value, err)
				}
				events = append(events, label)
			}
		}
		if err := future.ThenOwner(scope, run("first"), func(err error) { t.Error(err) }); err != nil {
			return err
		}
		if err := future.ThenOwner(scope, run("second"), func(err error) { t.Error(err) }); err != nil {
			return err
		}
		h.s.settle(scope, f, result{raw: raw("original")})
		h.s.settle(scope, f, result{raw: raw("forbidden second settlement")})
		if err := future.ThenOwner(scope, run("hot"), func(err error) { t.Error(err) }); err != nil {
			return err
		}
		return scope.AwaitFulfilled()
	})
	if fmt.Sprint(events) != "[first second hot]" {
		t.Fatal(events)
	}
	if err := future.ThenOwner(stale, func(*cooperative.Scope, string, error) { t.Error("stale callback") }, func(error) {}); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	other := cooperative.New()
	defer other.Close()
	if err := other.Run(context.Background(), func(scope *cooperative.Scope) error {
		err := future.ThenOwner(scope, func(*cooperative.Scope, string, error) { t.Error("foreign callback") }, func(error) {})
		if !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.call(t, func(scope *cooperative.Scope) error {
		if !errors.Is(future.ThenOwner(scope, nil, func(error) {}), cooperative.ErrRestricted) {
			t.Error("nil reaction")
		}
		f := h.s.future()
		original := errors.New("original filesystem rejection")
		if err := (&BranchFuture{f}).ThenOwner(scope, func(_ *cooperative.Scope, value string, err error) {
			if value != "" || err != original {
				t.Errorf("error identity %v %q", err, value)
			}
		}, func(err error) { t.Error(err) }); err != nil {
			return err
		}
		h.s.settle(scope, f, result{err: original})
		if _, err := f.Await(scope); err != original {
			return fmt.Errorf("await error identity %v", err)
		}
		return nil
	})
}

func TestOwnerCloseRejectsGitPromiseDependentsWithoutBusinessCallbacks(t *testing.T) {
	owner := cooperative.New()
	s := &Service{options: Options{Owner: owner}}
	var future *BranchFuture
	rejected := make(chan error, 1)
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		future = &BranchFuture{s.future()}
		return future.ThenOwner(scope, func(*cooperative.Scope, string, error) { t.Error("closed owner executed Git reaction") }, func(err error) { rejected <- err })
	}); err != nil {
		t.Fatal(err)
	}
	owner.Close()
	if err := <-rejected; !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := future.Wait(context.Background()); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal(err)
	}
	s.Join()
}

func TestAdministrativeCloseDoesNotReopenAfterStartedHEAD(t *testing.T) {
	entered, release, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var held atomic.Bool
	var onceHeld, onceStop, onceRelease sync.Once
	unblock := func() { onceRelease.Do(func() { close(release) }) }
	h := newHarness(t, func(o *Options) {
		o.ReadFile = func(path string) ([]byte, error) {
			data, err := os.ReadFile(path)
			if held.Load() && filepath.Base(path) == "HEAD" {
				onceHeld.Do(func() { close(entered); <-release })
			}
			return data, err
		}
		watch := o.WatchFile
		o.WatchFile = func(path string, interval time.Duration, notify func()) (func(), error) {
			stop, err := watch(path, interval, notify)
			if err != nil {
				return nil, err
			}
			return func() {
				stop()
				if filepath.Base(path) == "HEAD" {
					onceStop.Do(func() { close(stopped) })
				}
			}, nil
		}
	})
	t.Cleanup(unblock)
	h.write(t, ".git/HEAD", "ref: refs/heads/original\n")
	if h.branch(t) != "original" {
		t.Fatal("original branch")
	}
	held.Store(true)
	h.write(t, ".git/HEAD", "ref: refs/heads/changed\n")
	h.signal(t, ".git/HEAD")
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- h.s.Close(context.Background()) }()
	<-stopped // actual Source-unwatch callback inside administrative Close.
	h.call(t, func(scope *cooperative.Scope) error {
		if _, err := h.s.Branch(scope); !errors.Is(err, cooperative.ErrClosed) {
			return fmt.Errorf("close admission %v", err)
		}
		return nil
	})
	unblock()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	count := len(h.notify)
	h.mu.Unlock()
	if count != 0 {
		t.Fatalf("late HEAD reaction reopened %d watched files", count)
	}
}
