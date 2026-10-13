// Package skillwatcher owns the original skill detector's stat-polling,
// write-stability and batched invalidation lifecycle. It never runs a Harness.
package skillwatcher

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type Timing struct {
	Interval, Stability, StabilityPoll, Debounce, Atomic time.Duration
}
type Options struct {
	Paths []string
	// Native entry points reconcile these exact missing targets on stat polls.
	// No parent tree is watched; unrelated config/home files remain excluded.
	ReconcileMissingPaths bool
	// Reload receives one representative path per batch. Dynamic loads bypass
	// ConfigChange and disk-cache invalidation, exactly as the original signal.
	Reload  func(context.Context, string, bool) error
	OnError func(error)
	Timing  *Timing // External timer primitive for deterministic fixtures only.
}
type Watcher struct {
	cancel  context.CancelFunc
	done    chan struct{}
	dynamic chan struct{}
	once    sync.Once
}
type stamp struct {
	size     int64
	modified time.Time
}
type write struct {
	stamp            stamp
	changed, checked time.Time
	added            bool
}
type job struct {
	path    string
	dynamic bool
}

func Start(o Options) (*Watcher, error) {
	if o.Reload == nil {
		return nil, errors.New("skill watcher requires the owning command-cache consumer")
	}
	timing := Timing{2 * time.Second, time.Second, 500 * time.Millisecond, 300 * time.Millisecond, 100 * time.Millisecond}
	if o.Timing != nil {
		timing = *o.Timing
	}
	if timing.Interval <= 0 || timing.Stability <= 0 || timing.StabilityPoll <= 0 || timing.Debounce <= 0 || timing.Atomic <= 0 {
		return nil, errors.New("skill watcher timers must be positive")
	}
	paths := []string{}
	for _, path := range o.Paths {
		if !filepath.IsAbs(path) {
			return nil, errors.New("skill watcher paths must be absolute")
		}
		// Source only includes paths that existed when initialize ran.
		if info, e := os.Stat(path); e == nil && (info.Mode().IsRegular() || info.IsDir()) || o.ReconcileMissingPaths && errors.Is(e, fs.ErrNotExist) {
			paths = append(paths, path)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &Watcher{cancel: cancel, done: make(chan struct{}), dynamic: make(chan struct{}, 1)}
	initial := scan(paths)
	go w.run(ctx, o, timing, paths, initial)
	return w, nil
}
func (w *Watcher) NotifyDynamic() {
	select {
	case <-w.done:
		return
	default:
	}
	select {
	case w.dynamic <- struct{}{}:
	default:
	}
}
func (w *Watcher) Close() {
	if w != nil {
		w.once.Do(w.cancel)
		<-w.done
	}
}

func scan(roots []string) map[string]stamp {
	files := map[string]stamp{}
	var visit func(string, int)
	visit = func(path string, depth int) {
		for _, part := range strings.Split(path, string(filepath.Separator)) {
			if part == ".git" {
				return
			}
		}
		info, e := os.Stat(path)
		if e != nil {
			return
		} // Original ignorePermissionErrors and disappeared paths.
		if info.Mode().IsRegular() {
			files[path] = stamp{info.Size(), info.ModTime()}
			return
		}
		if !info.IsDir() || depth > 2 {
			return
		}
		entries, e := os.ReadDir(path)
		if e != nil {
			return
		}
		for _, entry := range entries {
			visit(filepath.Join(path, entry.Name()), depth+1)
		}
	}
	for _, root := range roots {
		visit(root, 0)
	}
	return files
}
func (w *Watcher) run(ctx context.Context, o Options, timing Timing, paths []string, previous map[string]stamp) {
	defer close(w.done)
	jobs := make(chan job, 1)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case next := <-jobs:
				if ctx.Err() != nil {
					return
				}
				if e := o.Reload(ctx, next.path, next.dynamic); e != nil && ctx.Err() == nil && o.OnError != nil {
					o.OnError(e)
				}
			}
		}
	}()
	defer func() { w.cancel(); <-workerDone }()
	poll := time.NewTicker(timing.Interval)
	defer poll.Stop()
	step := min(timing.StabilityPoll, timing.Debounce, timing.Atomic)
	clock := time.NewTicker(step)
	defer clock.Stop()
	writes := map[string]write{}
	unlinks := map[string]time.Time{}
	pending := []string{}
	var reloadAt time.Time
	ready := func(path string, now time.Time) {
		if !slices.Contains(pending, path) {
			pending = append(pending, path)
		}
		reloadAt = now.Add(timing.Debounce)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.dynamic:
			select {
			case jobs <- job{dynamic: true}:
			case <-ctx.Done():
				return
			}
		case now := <-poll.C:
			current := scan(paths)
			keys := make([]string, 0, len(current))
			for path := range current {
				keys = append(keys, path)
			}
			slices.Sort(keys)
			for _, path := range keys {
				value := current[path]
				old, exists := previous[path]
				if exists && old == value {
					continue
				}
				_, atomic := unlinks[path]
				delete(unlinks, path)
				pendingWrite, waiting := writes[path]
				if !waiting {
					pendingWrite = write{stamp: value, added: !exists && !atomic, checked: now}
				}
				pendingWrite.changed = now
				writes[path] = pendingWrite
			}
			for path := range previous {
				if _, exists := current[path]; !exists {
					pendingWrite, waiting := writes[path]
					delete(writes, path)
					if !waiting || !pendingWrite.added {
						unlinks[path] = now.Add(timing.Atomic)
					}
				}
			}
			previous = current
		case now := <-clock.C:
			keys := make([]string, 0, len(writes))
			for path := range writes {
				keys = append(keys, path)
			}
			slices.Sort(keys)
			for _, path := range keys {
				pendingWrite := writes[path]
				if now.Sub(pendingWrite.checked) < timing.StabilityPoll {
					continue
				}
				info, e := os.Stat(path)
				if e != nil {
					if !errors.Is(e, fs.ErrNotExist) && o.OnError != nil {
						o.OnError(e)
					}
					continue
				}
				if !info.Mode().IsRegular() {
					delete(writes, path)
					continue
				}
				if info.Size() != pendingWrite.stamp.size {
					pendingWrite.changed = now
				}
				pendingWrite.stamp = stamp{info.Size(), info.ModTime()}
				pendingWrite.checked = now
				if now.Sub(pendingWrite.changed) >= timing.Stability {
					delete(writes, path)
					ready(path, now)
				} else {
					writes[path] = pendingWrite
				}
			}
			keys = keys[:0]
			for path, at := range unlinks {
				if !now.Before(at) {
					keys = append(keys, path)
				}
			}
			slices.Sort(keys)
			for _, path := range keys {
				delete(unlinks, path)
				ready(path, now)
			}
			if len(pending) > 0 && !now.Before(reloadAt) {
				next := job{path: pending[0]}
				pending = nil
				select {
				case jobs <- next:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}
