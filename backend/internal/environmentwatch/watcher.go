// Package environmentwatch owns the original process FileChanged watcher.
// Hook selection and execution stay in the shared native Hook service.
package environmentwatch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/fsnotify/fsnotify"
)

type Options struct {
	CWD      string
	Snapshot func() (protocol.JSONObject, error)
	Event    func(context.Context, string, string) error
	Error    func(error)
}
type stamp struct {
	size     int64
	modified int64
	mode     os.FileMode
}
type pending struct {
	event  string
	stamp  stamp
	stable time.Time
}
type Watcher struct {
	mu              sync.Mutex
	options         Options
	watcher         *fsnotify.Watcher
	ctx             context.Context
	cancel          context.CancelFunc
	done            chan struct{}
	wake            chan struct{}
	jobs            sync.WaitGroup
	closed          bool
	dynamic, sorted []string
	targets         []string
	directories     map[string]bool
	known           map[string]stamp
	pending         map[string]pending
}

func ResolvePaths(cwd string, config protocol.JSONObject, dynamic []string) []string {
	result := []string{}
	seen := map[string]bool{}
	appendPath := func(path string) {
		if !seen[path] {
			seen[path] = true
			result = append(result, path)
		}
	}
	var matchers []protocol.JSONObject
	_ = json.Unmarshal(config.Get("FileChanged"), &matchers)
	for _, matcher := range matchers {
		for _, name := range strings.Split(matcher.String("matcher"), "|") {
			name = strings.TrimFunc(name, protocol.IsJavaScriptWhitespace)
			if name == "" {
				continue
			}
			if !filepath.IsAbs(name) {
				name = filepath.Join(cwd, name)
			}
			appendPath(name)
		}
	}
	for _, path := range dynamic {
		appendPath(path)
	}
	return result
}

func Start(options Options) (*Watcher, error) {
	if !filepath.IsAbs(options.CWD) || options.Snapshot == nil || options.Event == nil {
		return nil, errors.New("environment watcher requires actual cwd, snapshot and Hook executor")
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &Watcher{options: options, ctx: ctx, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1), directories: map[string]bool{}, known: map[string]stamp{}, pending: map[string]pending{}}
	w.mu.Lock()
	err := w.restartLocked()
	w.mu.Unlock()
	if err != nil {
		cancel()
		if w.watcher != nil {
			_ = w.watcher.Close()
		}
		return nil, err
	}
	go w.loop()
	return w, nil
}

func (w *Watcher) Update(paths []string) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	sorted := append([]string{}, paths...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := utf16.Encode([]rune(sorted[i])), utf16.Encode([]rune(sorted[j]))
		for n := 0; n < len(a) && n < len(b); n++ {
			if a[n] != b[n] {
				return a[n] < b[n]
			}
		}
		return len(a) < len(b)
	})
	equal := len(sorted) == len(w.sorted)
	if equal {
		for i, p := range sorted {
			if p != w.sorted[i] {
				equal = false
				break
			}
		}
	}
	if equal {
		return nil
	}
	w.dynamic = append([]string{}, paths...)
	w.sorted = sorted
	return w.restartLocked()
}

// CwdChanged replaces the dynamic pool even when it is empty; FileChanged's
// caller only replaces a nonempty pool. This distinction is Source behavior.
func (w *Watcher) CwdChanged(cwd string, paths []string) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.options.CWD = cwd
	w.dynamic = append([]string{}, paths...)
	w.sorted = append([]string{}, paths...)
	sort.SliceStable(w.sorted, func(i, j int) bool { return utf16Less(w.sorted[i], w.sorted[j]) })
	return w.restartLocked()
}

func utf16Less(aText, bText string) bool {
	a, b := utf16.Encode([]rune(aText)), utf16.Encode([]rune(bText))
	for n := 0; n < len(a) && n < len(b); n++ {
		if a[n] != b[n] {
			return a[n] < b[n]
		}
	}
	return len(a) < len(b)
}

func (w *Watcher) restartLocked() error {
	config, err := w.options.Snapshot()
	if err != nil {
		return err
	}
	w.targets = ResolvePaths(w.options.CWD, config, w.dynamic)
	if w.watcher != nil {
		_ = w.watcher.Close()
		w.watcher = nil
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
	if len(w.targets) == 0 {
		w.known = map[string]stamp{}
		w.directories = map[string]bool{}
		w.pending = map[string]pending{}
		return nil
	}
	w.watcher, err = fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.known, w.directories = w.scanLocked()
	w.pending = map[string]pending{}
	for dir := range w.directories {
		_ = w.watcher.Add(dir)
	}
	return nil
}

func (w *Watcher) scanLocked() (map[string]stamp, map[string]bool) {
	states := map[string]stamp{}
	dirs := map[string]bool{}
	addFile := func(path string, info os.FileInfo) {
		if !info.IsDir() {
			states[path] = stamp{info.Size(), info.ModTime().UnixNano(), info.Mode()}
		}
	}
	for _, target := range w.targets {
		path := target
		if !filepath.IsAbs(path) {
			path = filepath.Join(w.options.CWD, path)
		}
		nearest := filepath.Dir(path)
		for {
			if info, err := os.Stat(nearest); err == nil && info.IsDir() {
				dirs[nearest] = true
				break
			}
			parent := filepath.Dir(nearest)
			if parent == nearest {
				break
			}
			nearest = parent
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			addFile(path, info)
			continue
		}
		_ = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				dirs[p] = true
			} else {
				addFile(p, info)
			}
			return nil
		})
	}
	return states, dirs
}

func (w *Watcher) collect() [][2]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.watcher == nil {
		return nil
	}
	current, dirs := w.scanLocked()
	now := time.Now()
	events := [][2]string{}
	for dir := range dirs {
		if !w.directories[dir] {
			_ = w.watcher.Add(dir)
		}
	}
	for dir := range w.directories {
		if !dirs[dir] {
			_ = w.watcher.Remove(dir)
		}
	}
	w.directories = dirs
	for path, old := range w.known {
		if _, exists := current[path]; !exists {
			delete(w.pending, path)
			events = append(events, [2]string{path, "unlink"})
		} else {
			_ = old
		}
	}
	for path, state := range current {
		old, exists := w.known[path]
		p, waiting := w.pending[path]
		if !exists || old != state {
			event := "change"
			if !exists {
				event = "add"
			}
			if waiting {
				event = p.event
			}
			w.pending[path] = pending{event, state, now}
		} else if waiting && p.stamp == state && now.Sub(p.stable) >= 500*time.Millisecond {
			events = append(events, [2]string{path, p.event})
			delete(w.pending, path)
		}
	}
	w.known = current
	return events
}

func (w *Watcher) loop() {
	defer close(w.done)
	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()
	for {
		w.mu.Lock()
		var events <-chan fsnotify.Event
		var failures <-chan error
		var polling <-chan time.Time
		if w.watcher != nil {
			events = w.watcher.Events
			failures = w.watcher.Errors
			polling = poll.C
		}
		w.mu.Unlock()
		select {
		case <-w.ctx.Done():
			return
		case <-w.wake:
			continue
		case _, ok := <-events:
			if !ok {
				continue
			}
		case err, ok := <-failures:
			if !ok {
				continue
			}
			if w.options.Error != nil {
				w.options.Error(err)
			}
		case <-polling:
		}
		for _, event := range w.collect() {
			w.jobs.Add(1)
			go func(path, kind string) {
				defer w.jobs.Done()
				if err := w.options.Event(w.ctx, path, kind); err != nil && w.ctx.Err() == nil && w.options.Error != nil {
					w.options.Error(err)
				}
			}(event[0], event[1])
		}
	}
}

func (w *Watcher) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		w.cancel()
		if w.watcher != nil {
			_ = w.watcher.Close()
		}
	}
	w.mu.Unlock()
	<-w.done
	w.jobs.Wait()
}
