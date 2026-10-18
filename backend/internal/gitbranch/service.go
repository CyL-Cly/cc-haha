// Package gitbranch independently derives the pinned Source Git branch from
// original paths and native file primitives on the process's canonical owner.
package gitbranch

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"io/fs"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string { return "Git branch capability: " + e.Boundary }

type Options struct {
	Owner                *cooperative.Owner
	CWD                  func() string
	Now                  func() int64
	Diagnostics          *diagnostics.Process
	StatSync             func(string) (fs.FileInfo, error)
	Stat                 func(string) (fs.FileInfo, error)
	ReadFile             func(string) ([]byte, error)
	WatchFile            func(string, time.Duration, func()) (func(), error)
	WatchFileOwned       func(*cooperative.Scope, string, time.Duration, func(*cooperative.Scope)) (func(), error)
	ScrollDraining       func() bool
	RegisterCleanup      func(func(*cooperative.Scope) error)
	RegisterCleanupOwned func(*cooperative.Scope, func(*cooperative.Scope) error) error
	NodeEnv              string
	// ObserveWatchCallbackEnd is an optional host completion capability. It
	// runs at the end of the actual callback body on this Service's Owner,
	// after dirty/HEAD admission, including its early returns. It does not
	// await the later asynchronous ref update or provide setImmediate.
	ObserveWatchCallbackEnd func(*cooperative.Scope, string)
}
type result struct {
	raw json.RawMessage
	err error
}
type Future struct {
	mu        sync.Mutex // Promise registration/administrative rejection, not Git policy.
	owner     *cooperative.Owner
	service   *Service
	done      chan struct{}
	settled   bool
	value     result
	listeners []reaction
}

func (f *Future) Done() <-chan struct{} { return f.done }
func (f *Future) Wait(ctx context.Context) (json.RawMessage, error) {
	if ctx == nil {
		return nil, errors.New("Git future requires observer context")
	}
	select {
	case <-f.done:
		return append(json.RawMessage(nil), f.value.raw...), f.value.err
	default:
	}
	select {
	case <-f.done:
		return append(json.RawMessage(nil), f.value.raw...), f.value.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}
func (f *Future) Await(scope *cooperative.Scope) (json.RawMessage, error) {
	if !scope.BelongsTo(f.owner) {
		return nil, cooperative.ErrRestricted
	}
	var value result
	done := make(chan struct{})
	var once sync.Once
	if err := f.register(scope, reaction{run: func(_ *cooperative.Scope, r result) { value = r; once.Do(func() { close(done) }) }, rejected: func(err error) { value = result{err: err}; once.Do(func() { close(done) }) }}); err != nil {
		return nil, err
	}
	if err := scope.AwaitPromise(done); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), value.raw...), value.err
}

type BranchFuture struct{ *Future }

func (f *BranchFuture) Wait(ctx context.Context) (string, error) {
	raw, e := f.Future.Wait(ctx)
	if e != nil {
		return "", e
	}
	var s string
	e = json.Unmarshal(raw, &s)
	return s, e
}
func (f *BranchFuture) Await(scope *cooperative.Scope) (string, error) {
	raw, e := f.Future.Await(scope)
	if e != nil {
		return "", e
	}
	var s string
	e = json.Unmarshal(raw, &s)
	return s, e
}

// ThenOwner represents Source's actual await getCachedBranch() reaction. It
// retains registration FIFO even on the hot path. onClosed is administrative
// dependent-promise rejection only and must not execute business/Frame work.
func (f *BranchFuture) ThenOwner(scope *cooperative.Scope, run func(*cooperative.Scope, string, error), onClosed func(error)) error {
	if f == nil || f.Future == nil || run == nil || onClosed == nil {
		return cooperative.ErrRestricted
	}
	return f.register(scope, reaction{run: func(next *cooperative.Scope, r result) {
		var value string
		err := r.err
		if err == nil {
			err = json.Unmarshal(r.raw, &value)
		}
		run(next, value, err)
	}, rejected: onClosed})
}

type rootEntry struct{ start, root string }
type dirValue struct{ path string }
type cacheEntry struct {
	value json.RawMessage
	dirty bool
}
type Service struct {
	// Optional read-only observer at the actual computeBranch return boundary.
	// It only registers observations on this current Owner; nil in production.
	observeBranchComputeReturn func(*cooperative.Scope)
	options                    Options
	roots                      []rootEntry
	dirs                       map[string]dirValue
	initialized                bool
	init                       *Future
	gitDir, commonDir          *string
	refPath                    string
	watched                    map[string]func()
	branch                     *cacheEntry
	defaultBranch              *cacheEntry
	workers                    sync.WaitGroup
	closed                     bool
	watchFault                 error
}

func New(o Options) (*Service, error) {
	if o.Owner == nil || o.CWD == nil || o.Now == nil || o.StatSync == nil || o.Stat == nil || o.ReadFile == nil || (o.WatchFile == nil && o.WatchFileOwned == nil) || o.ScrollDraining == nil || (o.RegisterCleanup == nil && o.RegisterCleanupOwned == nil) {
		return nil, errors.New("Git branch requires canonical owner and actual original path/Clock/files/watch/scroll/cleanup capabilities")
	}
	if o.WatchFile != nil && o.WatchFileOwned != nil {
		return nil, &CapabilityError{"multiple watch registration capabilities"}
	}
	if o.RegisterCleanup != nil && o.RegisterCleanupOwned != nil {
		return nil, &CapabilityError{"multiple cleanup registration capabilities"}
	}
	return &Service{options: o, dirs: map[string]dirValue{}, watched: map[string]func(){}}, nil
}

// OwnsOwner checks immutable construction identity only. It does not read
// closed/cache/watch state, start Git, or consume a native path.
func (s *Service) OwnsOwner(owner *cooperative.Owner) bool {
	return s != nil && owner != nil && s.options.Owner == owner
}

func (s *Service) check(scope *cooperative.Scope) error {
	if !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	if s.closed {
		return cooperative.ErrClosed
	}
	return nil
}

// A Future owns its promise settlement independently of an ASK observer. The
// Owner-close observer rejects unresolved promises administratively; it never
// performs Git reductions, file operations, or a provider Frame.
func (s *Service) future() *Future {
	f := &Future{owner: s.options.Owner, service: s, done: make(chan struct{})}
	go func() {
		select {
		case <-f.done:
		case <-f.owner.Done():
			f.reject(cooperative.ErrClosed)
		}
	}()
	return f
}
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

type reaction struct {
	run      func(*cooperative.Scope, result)
	rejected func(error)
}

func (f *Future) queue(r reaction, value result) {
	f.service.workers.Add(1)
	receipt, err := f.owner.Microtask(cooperative.Read, func(scope *cooperative.Scope) error { r.run(scope, value); return nil })
	if err != nil {
		f.service.workers.Done()
		if r.rejected != nil {
			r.rejected(err)
		}
		return
	}
	go func() {
		defer f.service.workers.Done()
		if err := receipt.Wait(); err != nil && r.rejected != nil {
			r.rejected(err)
		}
	}()
}
func (f *Future) register(scope *cooperative.Scope, r reaction) error {
	if !scope.BelongsTo(f.owner) || r.run == nil {
		return cooperative.ErrRestricted
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.settled {
		f.queue(r, f.value)
	} else {
		f.listeners = append(f.listeners, r)
	}
	return nil
}
func (f *Future) reject(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.settled {
		return
	}
	f.settled = true
	f.value = result{err: err}
	// No business callback executes on this administrative close path.
	for _, r := range f.listeners {
		if r.rejected != nil {
			r.rejected(err)
		}
	}
	f.listeners = nil
	close(f.done)
}

// post is only an already-admitted filesystem/watch completion. Promise
// reactions use Microtask separately, including a fulfilled async idle return.
func (s *Service) post(f *Future, cb func(*cooperative.Scope)) {
	s.workers.Add(1)
	receipt, err := s.options.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error { cb(scope); return nil })
	if err != nil {
		s.workers.Done()
		if f != nil {
			f.reject(err)
		}
		return
	}
	go func() {
		defer s.workers.Done()
		if err := receipt.Wait(); err != nil && f != nil {
			f.reject(err)
		}
	}()
}
func (s *Service) rejectClosed(f *Future, err error) { f.reject(err) }
func (s *Service) settle(_ *cooperative.Scope, f *Future, r result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.settled {
		return
	}
	f.settled = true
	r.raw = append(json.RawMessage(nil), r.raw...)
	f.value = r
	// Promise reactions already registered precede observers of Done. Queueing
	// under the registration mutex preserves FIFO with concurrent close observers.
	for _, cb := range f.listeners {
		f.queue(cb, r)
	}
	f.listeners = nil
	close(f.done)
}
func (s *Service) then(scope *cooperative.Scope, f *Future, cb func(*cooperative.Scope, result)) {
	_ = f.register(scope, reaction{run: cb})
}

type physical struct {
	bytes []byte
	info  fs.FileInfo
	err   error
}

func (s *Service) io(f *Future, path string, stat bool, cb func(*cooperative.Scope, physical)) {
	read, statFn := s.options.ReadFile, s.options.Stat
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		v := physical{}
		if stat {
			v.info, v.err = statFn(path)
		} else {
			v.bytes, v.err = read(path)
			v.bytes = append([]byte(nil), v.bytes...)
		}
		receipt, e := s.options.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error { cb(scope, v); return nil })
		if e != nil {
			s.rejectClosed(f, e)
			return
		}
		if e := receipt.Wait(); e != nil {
			s.rejectClosed(f, e)
		}
	}()
}

// Join is off-owner administrative teardown, after stopping native admissions.
// It is not Source unwatch/reset or a model cancellation receipt.
func (s *Service) Join() { s.workers.Wait() }
func (s *Service) Cleanup(scope *cooperative.Scope) error {
	if e := s.check(scope); e != nil {
		return e
	}
	for p, stop := range s.watched {
		stop()
		delete(s.watched, p)
	}
	s.refPath = ""
	return nil
}
func (s *Service) Reset(scope *cooperative.Scope) error {
	if e := s.Cleanup(scope); e != nil {
		return e
	}
	s.branch = nil
	s.defaultBranch = nil
	s.initialized = false
	s.init = nil
	s.gitDir = nil
	s.commonDir = nil
	s.watchFault = nil
	return nil
}
func (s *Service) ClearDirs(scope *cooperative.Scope) error {
	if e := s.check(scope); e != nil {
		return e
	}
	clear(s.dirs)
	return nil
}
func (s *Service) ClearRoots(scope *cooperative.Scope) error {
	if e := s.check(scope); e != nil {
		return e
	}
	s.roots = nil
	return nil
}
