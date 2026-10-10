package gitbranch

import (
	"context"
	"errors"
	"io/fs"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// NativePollWatch is a finite external filesystem capability. It emits change
// notifications only; branch/cache/ref decisions run on Service's owner. It is
// not a claim of universal Bun StatWatcher scheduling or nanosecond equivalence.
// The returned unwatch does not join an already started stat/callback; Join does.
type NativePollWatch struct {
	Owner   *cooperative.Owner
	Stat    func(string) (fs.FileInfo, error)
	workers sync.WaitGroup
}

// Watch preserves the legacy arbitrary listener contract: registration never
// waits for its initial ENOENT callback. A Service should use WatchOwned instead
// so already-completed readiness is admitted on its canonical owner immediately.
func (w *NativePollWatch) Watch(path string, interval time.Duration, notify func()) (func(), error) {
	prior, e := w.Stat(path)
	if e != nil {
		prior = nil
	}
	initialENOENT := prior == nil && errors.Is(e, fs.ErrNotExist)
	if prior == nil && !initialENOENT {
		notify()
	}
	return w.poll(path, interval, prior, func() { notify() }, initialENOENT), nil
}

// WatchOwned admits the result of the original synchronous registration stat
// before a caller can dispatch its subsequent native read. The callback still
// runs asynchronously, on the existing owner; no listener runs on the worker.
// This is the finite pinned initial-ENOENT contract, not a universal poll phase.
func (w *NativePollWatch) WatchOwned(scope *cooperative.Scope, path string, interval time.Duration, notify func(*cooperative.Scope)) (func(), error) {
	if w == nil || w.Owner == nil || !scope.BelongsTo(w.Owner) || w.Stat == nil || notify == nil {
		return nil, cooperative.ErrRestricted
	}
	deliver := func() error {
		receipt, e := w.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error { notify(next); return nil })
		if e != nil {
			return e
		}
		w.workers.Add(1)
		go func() { defer w.workers.Done(); _ = receipt.Wait() }()
		return nil
	}
	prior, e := w.Stat(path)
	if e != nil {
		prior = nil
	}
	if prior == nil {
		if e := deliver(); e != nil {
			return nil, e
		}
	}
	return w.poll(path, interval, prior, func() { _ = deliver() }, false), nil
}

func (w *NativePollWatch) poll(path string, interval time.Duration, prior fs.FileInfo, notify func(), initial bool) func() {
	stop := make(chan struct{})
	var once sync.Once
	w.workers.Add(1)
	go func() {
		defer w.workers.Done()
		if initial {
			notify()
		}
		timer := time.NewTicker(interval)
		defer timer.Stop()
		for {
			select {
			case <-stop:
				return
			case <-timer.C:
				next, e := w.Stat(path)
				if e != nil {
					next = nil
				}
				changed := prior == nil && next != nil || prior != nil && next == nil || prior != nil && next != nil && (prior.ModTime() != next.ModTime() || prior.Size() != next.Size() || prior.Mode() != next.Mode())
				prior = next
				if changed {
					notify()
				}
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}
func (w *NativePollWatch) Join() { w.workers.Wait() }
