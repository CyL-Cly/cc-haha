package localindex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type ReconciliationBatch struct {
	Paths     []string `json:"paths"`
	FullSweep bool     `json:"fullSweep"`
}
type WatchMetrics struct {
	QueuedPaths   int `json:"queuedPaths"`
	MaxBatchSize  int `json:"maxBatchSize"`
	Yielded       int `json:"yielded"`
	FullSweeps    int `json:"fullSweeps"`
	WatchFailures int `json:"watchFailures"`
}
type WatchOptions struct {
	Scope                                               string
	OnBatch                                             func(context.Context, ReconciliationBatch) error
	OnDirty                                             func()
	OnFailure                                           func()
	OnRecovered                                         func()
	IsTargetPath                                        func(string, string) bool
	Debounce, MaxWait, SafetySweep, RetryBase, RetryMax time.Duration
	MaxQueuedPaths, BatchSize                           int
	ListDirectories                                     func(context.Context, string) ([]string, error)
}
type Watcher struct {
	o             WatchOptions
	mu            sync.Mutex
	lifecycleMu   sync.Mutex
	active        bool
	generation    int64
	dirty         map[string]bool
	full          bool
	pending       *ReconciliationBatch
	metrics       WatchMetrics
	cancel        context.CancelFunc
	done          chan struct{}
	wake, process chan struct{}
	native        *fsnotify.Watcher
	watches       map[string]bool
}

func NewWatcher(o WatchOptions) (*Watcher, error) {
	if !filepath.IsAbs(o.Scope) || o.OnBatch == nil {
		return nil, errors.New("Index watcher requires an absolute scope and actual reconciliation producer")
	}
	if o.Debounce <= 0 {
		o.Debounce = 350 * time.Millisecond
	}
	if o.MaxWait < o.Debounce {
		o.MaxWait = 2 * time.Second
		if o.MaxWait < o.Debounce {
			o.MaxWait = o.Debounce
		}
	}
	if o.SafetySweep <= 0 {
		o.SafetySweep = 5 * time.Minute
	}
	if o.RetryBase <= 0 {
		o.RetryBase = time.Second
	}
	if o.RetryMax < o.RetryBase {
		o.RetryMax = time.Minute
		if o.RetryMax < o.RetryBase {
			o.RetryMax = o.RetryBase
		}
	}
	if o.MaxQueuedPaths <= 0 {
		o.MaxQueuedPaths = 2048
	}
	if o.BatchSize <= 0 {
		o.BatchSize = 25
	}
	o.BatchSize = min(25, o.BatchSize)
	if o.IsTargetPath == nil {
		o.IsTargetPath = IsPrimaryTranscriptPath
	}
	if o.ListDirectories == nil {
		o.ListDirectories = listWatchDirectories
	}
	return &Watcher{o: o, dirty: map[string]bool{}, watches: map[string]bool{}}, nil
}
func IsPrimaryTranscriptPath(scope, path string) bool {
	parts := pathParts(scope, path)
	return len(parts) == 2
}
func pathParts(scope, path string) []string {
	root := filepath.Join(scope, "projects")
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	child, err := filepath.Rel(root, absolute)
	if err != nil || child == "." || child == ".." || strings.HasPrefix(child, ".."+string(filepath.Separator)) || filepath.IsAbs(child) {
		return nil
	}
	parts := strings.Split(child, string(filepath.Separator))
	if len(parts) == 0 || !strings.HasSuffix(parts[len(parts)-1], ".jsonl") || parts[len(parts)-1] == ".jsonl" {
		return nil
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return nil
		}
	}
	return parts
}
func IsContentTranscriptPath(scope, path string) bool { return pathParts(scope, path) != nil }
func listWatchDirectories(ctx context.Context, scope string) ([]string, error) {
	out := []string{}
	if info, err := os.Stat(scope); err == nil && info.IsDir() {
		out = append(out, scope)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	root := filepath.Join(scope, "projects")
	if info, err := os.Stat(root); os.IsNotExist(err) || err == nil && !info.IsDir() {
		return out, nil
	} else if err != nil {
		return nil, err
	}
	err := filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e.IsDir() {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}
func (w *Watcher) Start(ctx context.Context) error {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()
	w.mu.Lock()
	if w.active {
		w.mu.Unlock()
		return nil
	}
	native, err := fsnotify.NewWatcher()
	if err != nil {
		w.mu.Unlock()
		return err
	}
	workerctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	w.active = true
	w.generation++
	generation := w.generation
	w.cancel = cancel
	w.done = make(chan struct{})
	w.wake = make(chan struct{}, 1)
	w.process = make(chan struct{}, 1)
	w.native = native
	w.watches = map[string]bool{}
	w.mu.Unlock()
	failed := w.refresh(workerctx)
	go w.loop(workerctx, generation, failed)
	return nil
}
func (w *Watcher) Stop(ctx context.Context) error {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()
	w.mu.Lock()
	if !w.active {
		done := w.done
		w.mu.Unlock()
		if done != nil {
			select {
			case <-done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	w.active = false
	w.generation++
	w.cancel()
	done := w.done
	native := w.native
	w.dirty = map[string]bool{}
	w.full = false
	w.pending = nil
	w.metrics.QueuedPaths = 0
	w.mu.Unlock()
	_ = native.Close()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (w *Watcher) queue(path string, full bool) {
	w.mu.Lock()
	if !w.active {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
	if w.o.OnDirty != nil {
		w.o.OnDirty()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.active {
		return
	}
	if full {
		w.full = true
		w.dirty = map[string]bool{}
	} else if !w.full {
		absolute, err := filepath.Abs(path)
		if err != nil {
			w.full = true
		} else {
			w.dirty[absolute] = true
		}
		if len(w.dirty) > w.o.MaxQueuedPaths {
			w.full = true
			w.dirty = map[string]bool{}
		}
	}
	w.metrics.QueuedPaths = len(w.dirty)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}
func (w *Watcher) QueuePath(path string) {
	if !w.o.IsTargetPath(w.o.Scope, path) {
		w.QueueFullSweep()
		return
	}
	w.queue(path, false)
}
func (w *Watcher) QueueFullSweep()       { w.queue("", true) }
func (w *Watcher) Metrics() WatchMetrics { w.mu.Lock(); defer w.mu.Unlock(); return w.metrics }
func (w *Watcher) failure() {
	w.mu.Lock()
	w.metrics.WatchFailures++
	w.mu.Unlock()
	if w.o.OnFailure != nil {
		w.o.OnFailure()
	}
}
func (w *Watcher) refresh(ctx context.Context) bool {
	directories, err := w.o.ListDirectories(ctx, w.o.Scope)
	if err != nil {
		w.failure()
		return true
	}
	desired := map[string]bool{}
	for _, path := range directories {
		desired[filepath.Clean(path)] = true
	}
	for path := range w.watches {
		if !desired[path] {
			_ = w.native.Remove(path)
			delete(w.watches, path)
		}
	}
	failed := false
	for path := range desired {
		if w.watches[path] {
			continue
		}
		if err := w.native.Add(path); err != nil {
			failed = true
			w.failure()
			continue
		}
		w.watches[path] = true
	}
	return failed
}
func (w *Watcher) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.active {
		return
	}
	paths := []string{}
	if !w.full {
		for path := range w.dirty {
			paths = append(paths, path)
		}
		sort.Strings(paths)
	}
	full := w.full
	w.full = false
	w.dirty = map[string]bool{}
	w.metrics.QueuedPaths = 0
	if !full && len(paths) == 0 {
		return
	}
	if w.pending == nil {
		w.pending = &ReconciliationBatch{paths, full}
	} else if full {
		w.pending.FullSweep = true
		w.pending.Paths = nil
	} else if !w.pending.FullSweep {
		all := map[string]bool{}
		for _, p := range append(w.pending.Paths, paths...) {
			all[p] = true
		}
		if len(all) > w.o.MaxQueuedPaths {
			w.pending.FullSweep = true
			w.pending.Paths = nil
		} else {
			w.pending.Paths = nil
			for p := range all {
				w.pending.Paths = append(w.pending.Paths, p)
			}
			sort.Strings(w.pending.Paths)
		}
	}
	select {
	case w.process <- struct{}{}:
	default:
	}
}
func (w *Watcher) processPending(ctx context.Context) {
	for {
		w.mu.Lock()
		pending := w.pending
		w.pending = nil
		active := w.active
		w.mu.Unlock()
		if pending == nil || !active || ctx.Err() != nil {
			return
		}
		if pending.FullSweep {
			w.mu.Lock()
			w.metrics.FullSweeps++
			w.mu.Unlock()
			_ = w.o.OnBatch(ctx, *pending)
			continue
		}
		for start := 0; start < len(pending.Paths) && ctx.Err() == nil; start += w.o.BatchSize {
			end := min(start+w.o.BatchSize, len(pending.Paths))
			w.mu.Lock()
			w.metrics.MaxBatchSize = max(w.metrics.MaxBatchSize, end-start)
			if end < len(pending.Paths) {
				w.metrics.Yielded++
			}
			w.mu.Unlock()
			_ = w.o.OnBatch(ctx, ReconciliationBatch{Paths: pending.Paths[start:end]})
			if end < len(pending.Paths) {
				time.Sleep(0)
			}
		}
	}
}
func (w *Watcher) loop(ctx context.Context, generation int64, failed bool) {
	defer close(w.done)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.process:
				w.processPending(ctx)
			}
		}
	}()
	defer func() { <-workerDone }()
	safety := time.NewTicker(w.o.SafetySweep)
	defer safety.Stop()
	var debounce, maxWait, retry *time.Timer
	var dc, mc, rc <-chan time.Time
	retryDelay := w.o.RetryBase
	stopTimer := func(timer *time.Timer) {
		if timer != nil {
			timer.Stop()
		}
	}
	defer func() { stopTimer(debounce); stopTimer(maxWait); stopTimer(retry) }()
	scheduleRetry := func() {
		if rc != nil {
			return
		}
		retry = time.NewTimer(retryDelay)
		rc = retry.C
		retryDelay = min(w.o.RetryMax, retryDelay*2)
	}
	if failed {
		scheduleRetry()
		w.QueueFullSweep()
	}
	events, watchErrors := w.native.Events, w.native.Errors
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
			if mc == nil {
				maxWait = time.NewTimer(w.o.MaxWait)
				mc = maxWait.C
			}
			stopTimer(debounce)
			debounce = time.NewTimer(w.o.Debounce)
			dc = debounce.C
		case <-dc:
			dc = nil
			stopTimer(maxWait)
			mc = nil
			w.flush()
		case <-mc:
			mc = nil
			stopTimer(debounce)
			dc = nil
			w.flush()
		case <-safety.C:
			w.QueueFullSweep()
			if !failed {
				failed = w.refresh(ctx)
				if failed {
					scheduleRetry()
				}
			}
		case <-rc:
			rc = nil
			failed = w.refresh(ctx)
			if failed {
				scheduleRetry()
			} else {
				retryDelay = w.o.RetryBase
				if w.o.OnRecovered != nil {
					w.o.OnRecovered()
				}
				w.QueueFullSweep()
			}
		case _, ok := <-watchErrors:
			if !ok {
				watchErrors = nil
				continue
			}
			failed = true
			w.failure()
			w.QueueFullSweep()
			scheduleRetry()
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			path := event.Name
			if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				delete(w.watches, filepath.Clean(path))
			}
			parent := filepath.Dir(path)
			root := filepath.Join(w.o.Scope, "projects")
			if parent == w.o.Scope || parent == root || !w.o.IsTargetPath(w.o.Scope, path) {
				w.QueueFullSweep()
				if event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
					if w.refresh(ctx) {
						failed = true
						scheduleRetry()
					}
				}
			} else {
				w.QueuePath(path)
			}
		}
	}
}
