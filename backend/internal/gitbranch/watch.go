package gitbranch

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"time"
)

func (s *Service) Branch(scope *cooperative.Scope) (*BranchFuture, error) {
	return s.BranchWithCWD(scope, func(*cooperative.Scope) (string, error) { return s.options.CWD(), nil })
}

// BranchWithCWD carries the caller's immutable async context through the real
// ensureStarted and computeBranch continuations. The same service owns cache and
// watcher state; the getter is read at each original resolveGitDir call.
func (s *Service) BranchWithCWD(scope *cooperative.Scope, cwd func(*cooperative.Scope) (string, error)) (*BranchFuture, error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if s.watchFault != nil {
		return nil, s.watchFault
	}
	if cwd == nil {
		return nil, cooperative.ErrRestricted
	}
	f := s.future()
	started := s.ensureWithCWD(scope, cwd)
	s.then(scope, started, func(scope *cooperative.Scope, r result) {
		if r.err != nil {
			s.settle(scope, f, r)
			return
		}
		if s.watchFault != nil {
			s.settle(scope, f, result{err: s.watchFault})
			return
		}
		existing := s.branch
		if existing != nil && !existing.dirty {
			s.settle(scope, f, result{raw: existing.value})
			return
		}
		if existing != nil {
			existing.dirty = false
		}
		value, e := cwd(scope)
		if e != nil {
			s.settle(scope, f, result{err: e})
			return
		}
		resolved, e := s.Resolve(scope, value)
		if e != nil {
			s.settle(scope, f, result{err: e})
			return
		}
		s.then(scope, resolved, func(scope *cooperative.Scope, r result) {
			if r.err != nil {
				s.settle(scope, f, r)
				return
			}
			dir := stringValue(r.raw)
			finish := func(scope *cooperative.Scope, value string) {
				if s.observeBranchComputeReturn != nil {
					s.observeBranchComputeReturn(scope)
				}
				// computeBranch's async body has returned. GitFileWatcher.get
				// awaits that result before publishing its cache, even for HEAD
				// with no repository. This is a Promise reaction, not native work.
				computed := s.future()
				s.then(scope, computed, func(next *cooperative.Scope, r result) {
					if r.err != nil {
						s.settle(next, f, r)
						return
					}
					entry := s.branch
					if entry != nil && !entry.dirty {
						entry.value = r.raw
					}
					if entry == nil {
						s.branch = &cacheEntry{value: r.raw}
					}
					s.settle(next, f, r)
				})
				s.settle(scope, computed, result{raw: raw(value)})
			}
			if dir == "" {
				finish(scope, "HEAD")
				return
			}
			head := s.future()
			s.readHead(scope, head, dir)
			s.then(scope, head, func(scope *cooperative.Scope, r result) {
				if r.err != nil {
					s.settle(scope, f, r)
					return
				}
				var v struct{ Type, Name string }
				_ = json.Unmarshal(r.raw, &v)
				if v.Type == "branch" {
					finish(scope, v.Name)
				} else {
					finish(scope, "HEAD")
				}
			})
		})
	})
	return &BranchFuture{f}, nil
}
func (s *Service) ensure(scope *cooperative.Scope) *Future {
	return s.ensureWithCWD(scope, func(*cooperative.Scope) (string, error) { return s.options.CWD(), nil })
}
func (s *Service) ensureWithCWD(scope *cooperative.Scope, cwd func(*cooperative.Scope) (string, error)) *Future {
	if s.initialized {
		f := s.future()
		s.settle(scope, f, result{raw: raw(nil)})
		return f
	}
	if s.init != nil {
		return s.adoptStartReturn(scope, s.init)
	}
	return s.adoptStartReturn(scope, s.startWithCWD(scope, cwd))
}

// startWithCWD is GitFileWatcher's actual start body and single cached
// initPromise. ensureStarted's separate async return is owned by its caller.
func (s *Service) startWithCWD(scope *cooperative.Scope, cwd func(*cooperative.Scope) (string, error)) *Future {
	f := s.future()
	s.init = f
	value, e := cwd(scope)
	if e != nil {
		s.settle(scope, f, result{err: e})
		return f
	}
	resolved, e := s.Resolve(scope, value)
	if e != nil {
		s.settle(scope, f, result{err: e})
		return f
	}
	s.then(scope, resolved, func(scope *cooperative.Scope, r result) {
		if r.err != nil {
			s.settle(scope, f, r)
			return
		}
		s.gitDir = nullableDirectory(stringValue(r.raw))
		s.initialized = true
		if s.gitDir == nil || *s.gitDir == "" {
			s.settle(scope, f, result{raw: raw(nil)})
			return
		}
		s.common(scope, f, *s.gitDir, func(scope *cooperative.Scope, common string) {
			s.commonDir = nullableDirectory(common)
			headPath, e := joinNullableDirectory(s.gitDir, "HEAD")
			if e != nil {
				s.settle(scope, f, result{err: e})
				return
			}
			if e := s.watch(scope, headPath, true); e != nil {
				s.settle(scope, f, result{err: e})
				return
			}
			configDir := s.commonDir
			if configDir == nil {
				configDir = s.gitDir
			}
			configPath, e := joinNullableDirectory(configDir, "config")
			if e != nil {
				s.settle(scope, f, result{err: e})
				return
			}
			if e := s.watch(scope, configPath, false); e != nil {
				s.settle(scope, f, result{err: e})
				return
			}
			s.updateRef(scope, f, func(scope *cooperative.Scope, e error) {
				if e != nil {
					s.settle(scope, f, result{err: e})
					return
				}
				if s.options.RegisterCleanupOwned != nil {
					if e := s.options.RegisterCleanupOwned(scope, s.Cleanup); e != nil {
						s.settle(scope, f, result{err: e})
						return
					}
				} else {
					s.options.RegisterCleanup(s.Cleanup)
				}
				s.settle(scope, f, result{raw: raw(nil)})
			})
		})
	})
	return f
}
func (s *Service) watch(scope *cooperative.Scope, path string, head bool) error {
	// Source Cleanup unwatch remains non-cancelling. Administrative Close
	// separately prevents a late in-flight HEAD reaction reopening resources.
	if s.closed {
		return cooperative.ErrClosed
	}
	interval := time.Second
	if s.options.NodeEnv == "test" {
		interval = 10 * time.Millisecond
	}
	callback := func(scope *cooperative.Scope) {
		defer func() {
			if s.options.ObserveWatchCallbackEnd != nil {
				s.options.ObserveWatchCallbackEnd(scope, path)
			}
		}()
		if s.branch != nil {
			s.branch.dirty = true
		}
		if s.defaultBranch != nil {
			s.defaultBranch.dirty = true
		}
		if !head {
			return
		}
		if s.options.ScrollDraining() {
			s.watchFault = &CapabilityError{"active scroll-idle timer lifecycle is not migrated"}
			return
		}
		// Source awaits waitForScrollIdle(), whose inactive async return is
		// fulfilled. Resume through its actual Promise reaction.
		idle := s.future()
		s.settle(scope, idle, result{raw: raw(nil)})
		s.then(scope, idle, func(scope *cooperative.Scope, _ result) {
			f := s.future()
			s.updateRef(scope, f, func(scope *cooperative.Scope, e error) { s.watchFault = e; s.settle(scope, f, result{err: e}) })
		})
	}
	var stop func()
	var e error
	if s.options.WatchFileOwned != nil {
		stop, e = s.options.WatchFileOwned(scope, path, interval, callback)
	} else {
		stop, e = s.options.WatchFile(path, interval, func() { s.post(nil, callback) })
	}
	if e == nil {
		s.watched[path] = stop
	}
	return e
}
func (s *Service) updateRef(scope *cooperative.Scope, f *Future, done func(*cooperative.Scope, error)) {
	if s.gitDir == nil || *s.gitDir == "" {
		done(scope, nil)
		return
	}
	head := s.future()
	s.readHead(scope, head, *s.gitDir)
	s.then(scope, head, func(scope *cooperative.Scope, r result) {
		if r.err != nil {
			done(scope, r.err)
			return
		}
		var v struct{ Type, Name string }
		_ = json.Unmarshal(r.raw, &v)
		next := ""
		if v.Type == "branch" {
			dir := s.commonDir
			if dir == nil {
				dir = s.gitDir
			}
			var e error
			next, e = joinNullableDirectory(dir, "refs", "heads", v.Name)
			if e != nil {
				done(scope, e)
				return
			}
		}
		if next == s.refPath {
			done(scope, nil)
			return
		}
		if stop := s.watched[s.refPath]; stop != nil {
			stop()
			delete(s.watched, s.refPath)
		}
		s.refPath = next
		if next != "" {
			if e := s.watch(scope, next, false); e != nil {
				done(scope, e)
				return
			}
		}
		done(scope, nil)
	})
}

// No process owner is constructed by this service. Close is an external host
// operation; Source cleanup uses Cleanup and does not join already started I/O.
func (s *Service) Close(ctx context.Context) error {
	// Host teardown must stop native admissions even when its observer timed
	// out. Preserve that error after the original Owner's cleanup and I/O join.
	observerError := context.Cause(ctx)
	r, e := s.options.Owner.Submit(context.WithoutCancel(ctx), cooperative.Read, func(scope *cooperative.Scope) error {
		if e := s.Cleanup(scope); e != nil {
			return e
		}
		s.closed = true
		return nil
	})
	if e != nil {
		if errors.Is(e, cooperative.ErrClosed) {
			// No owner policy may run after Close. Wait for its final token before
			// releasing the already admitted physical subscriptions; cached branch,
			// ref and protocol state remain untouched on this administrative path.
			<-s.options.Owner.Done()
			for _, stop := range s.watched {
				stop()
			}
			s.Join()
		}
		return errors.Join(observerError, e)
	}
	e = r.Wait()
	s.Join()
	return errors.Join(observerError, e)
}
