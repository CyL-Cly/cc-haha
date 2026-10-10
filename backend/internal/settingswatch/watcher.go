// Package settingswatch ports settings/changeDetector.ts's actual native file
// and MDM change lifecycle. The owning MainPrint supplies cache/app consumers;
// this package never creates a session, tool registry or model query.
package settingswatch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/fsnotify/fsnotify"
)

type Timing struct{ Stability, Poll, DeletionGrace, Atomic, MDMPoll time.Duration }

var DefaultTiming = Timing{time.Second, 500 * time.Millisecond, 1700 * time.Millisecond, 100 * time.Millisecond, 30 * time.Minute}

type Options struct {
	Files           []SourceFile
	DropInDirectory string
	Remote          bool
	// Change combines original ConfigChange -> blocking check -> centralized
	// reset -> apply. The owner must not reset settings before Hook evaluation.
	Change               func(context.Context, string, string) error
	ConsumeInternalWrite func(string) bool
	ClearInternalWrites  func()
	// MDM's false read is the already-initialized native cache; true performs
	// physical refresh. Only a changed snapshot reaches the cache updater.
	MDM        func(context.Context, bool) (json.RawMessage, error)
	InstallMDM func(context.Context, json.RawMessage) error
	MDMChanged func(context.Context) error
	Error      func(error)
	Timing     *Timing
}
type Watcher struct {
	cancel  context.CancelFunc
	done    chan struct{}
	once    sync.Once
	targets Targets
}
type write struct {
	size             int64
	changed, checked time.Time
	added            bool
}

func Start(options Options) (*Watcher, error) {
	if options.Change == nil {
		return nil, errors.New("settings watcher requires actual cache/app-state consumer")
	}
	timing := DefaultTiming
	if options.Timing != nil {
		timing = *options.Timing
	}
	if timing.Stability <= 0 || timing.Poll <= 0 || timing.DeletionGrace <= 0 || timing.Atomic <= 0 || timing.MDMPoll <= 0 {
		return nil, errors.New("settings watcher timings must be positive")
	}
	ctx, cancel := context.WithCancel(context.Background())
	watcher := &Watcher{cancel: cancel, done: make(chan struct{})}
	if options.Remote {
		close(watcher.done)
		return watcher, nil
	}
	initial := json.RawMessage(nil)
	if options.MDM != nil {
		if options.MDMChanged == nil {
			cancel()
			return nil, errors.New("settings watcher requires original MDM notification consumer")
		}
		var err error
		initial, err = options.MDM(ctx, false)
		if err != nil {
			cancel()
			return nil, err
		}
		initial = protocol.StringifyValidatedJavaScriptJSON(initial)
	}
	targets, err := Collect(options.Files, options.DropInDirectory)
	if err != nil {
		cancel()
		return nil, err
	}
	watcher.targets = targets
	var native *fsnotify.Watcher
	if len(targets.Directories) > 0 {
		native, err = fsnotify.NewWatcher()
		if err != nil {
			cancel()
			return nil, err
		}
	}
	known := map[string]bool{}
	watched := map[string]bool{}
	add := func(path string) {
		if native == nil {
			return
		}
		if watched[path] {
			return
		}
		if err := native.Add(path); err == nil {
			watched[path] = true
		}
	}
	for _, directory := range targets.Directories {
		add(directory)
		add(filepath.Dir(directory))
	}
	for _, file := range targets.Files {
		if info, err := os.Stat(file.Path); err == nil && info.Mode().IsRegular() {
			known[file.Path] = true
		}
	}
	if targets.DropInDirectory != "" {
		entries, _ := os.ReadDir(targets.DropInDirectory)
		for _, entry := range entries {
			path := filepath.Join(targets.DropInDirectory, entry.Name())
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && targets.accepts(path) {
				known[path] = true
			}
		}
	}
	go watcher.run(ctx, options, timing, native, known, watched, initial)
	return watcher, nil
}
func (watcher *Watcher) Close() {
	if watcher != nil {
		watcher.once.Do(watcher.cancel)
		<-watcher.done
	}
}
func (watcher *Watcher) Targets() Targets {
	return Targets{Directories: append([]string{}, watcher.targets.Directories...), Files: append([]SourceFile{}, watcher.targets.Files...), DropInDirectory: watcher.targets.DropInDirectory}
}

func (watcher *Watcher) run(ctx context.Context, options Options, timing Timing, native *fsnotify.Watcher, known, watched map[string]bool, initial json.RawMessage) {
	defer close(watcher.done)
	var events <-chan fsnotify.Event
	var errorEvents <-chan error
	if native != nil {
		defer native.Close()
		events = native.Events
		errorEvents = native.Errors
	}
	var workers sync.WaitGroup
	defer func() {
		watcher.cancel()
		workers.Wait()
		if options.ClearInternalWrites != nil {
			options.ClearInternalWrites()
		}
	}()
	launch := func(task func() error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := task(); err != nil && ctx.Err() == nil && options.Error != nil {
				options.Error(err)
			}
		}()
	}
	changed := func(path string, deletion bool) {
		source := SourceForPath(path, options.Files, options.DropInDirectory)
		if source == "" {
			return
		}
		if !deletion && options.ConsumeInternalWrite != nil && options.ConsumeInternalWrite(path) {
			return
		}
		launch(func() error { return options.Change(ctx, source, path) })
	}
	writes := map[string]write{}
	deletions := map[string]time.Time{}
	poll := time.NewTicker(timing.Poll)
	defer poll.Stop()
	deleteClock := time.NewTicker(min(timing.Poll, timing.Atomic))
	defer deleteClock.Stop()
	var mdm <-chan time.Time
	var mdmTimer *time.Ticker
	if options.MDM != nil {
		mdmTimer = time.NewTicker(timing.MDMPoll)
		mdm = mdmTimer.C
		defer mdmTimer.Stop()
	}
	var mdmMu sync.Mutex
	lastMDM := string(initial)
	observe := func(path string, now time.Time) {
		if !watcher.targets.accepts(path) {
			return
		}
		info, err := os.Stat(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return
			}
			pending, writing := writes[path]
			delete(writes, path)
			if !known[path] || writing && pending.added {
				delete(known, path)
				return
			}
			delete(known, path)
			if _, exists := deletions[path]; !exists {
				deletions[path] = now.Add(timing.Atomic + timing.DeletionGrace)
			}
			return
		}
		if !info.Mode().IsRegular() {
			return
		}
		pending, exists := writes[path]
		if !exists {
			_, deleted := deletions[path]
			pending = write{added: !known[path] && !deleted, checked: now}
		}
		pending.changed = now
		pending.size = info.Size()
		writes[path] = pending
		known[path] = true
	}
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-errorEvents:
			if !ok {
				return
			}
			if options.Error != nil {
				options.Error(err)
			}
		case event, ok := <-events:
			if !ok {
				return
			}
			path := filepath.Clean(event.Name)
			now := time.Now()
			rootEvent := false
			for _, directory := range watcher.targets.Directories {
				if directory == path {
					rootEvent = true
					info, err := os.Stat(directory)
					if err == nil && info.IsDir() {
						if !watched[directory] {
							if native.Add(directory) == nil {
								watched[directory] = true
							}
						}
					} else {
						delete(watched, directory)
					}
					for file := range known {
						if filepath.Dir(file) == directory {
							observe(file, now)
						}
					}
					entries, _ := os.ReadDir(directory)
					for _, entry := range entries {
						observe(filepath.Join(directory, entry.Name()), now)
					}
				}
			}
			if !rootEvent {
				observe(path, now)
			}
		case now := <-poll.C:
			for path, pending := range writes {
				if now.Sub(pending.checked) < timing.Poll {
					continue
				}
				info, err := os.Stat(path)
				if err != nil {
					observe(path, now)
					continue
				}
				if !info.Mode().IsRegular() {
					delete(writes, path)
					continue
				}
				if info.Size() != pending.size {
					pending.changed = now
				}
				pending.size = info.Size()
				pending.checked = now
				if now.Sub(pending.changed) >= timing.Stability {
					delete(writes, path)
					delete(deletions, path)
					changed(path, false)
				} else {
					writes[path] = pending
				}
			}
		case now := <-deleteClock.C:
			for path, deadline := range deletions {
				if !now.Before(deadline) {
					delete(deletions, path)
					changed(path, true)
				}
			}
		case <-mdm:
			launch(func() error {
				snapshot, err := options.MDM(ctx, true)
				if err != nil {
					return err
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				snapshot = protocol.StringifyValidatedJavaScriptJSON(snapshot)
				mdmMu.Lock()
				different := lastMDM != string(snapshot)
				if different {
					lastMDM = string(snapshot)
				}
				mdmMu.Unlock()
				if different {
					if options.InstallMDM != nil {
						if err := options.InstallMDM(ctx, snapshot); err != nil {
							return err
						}
					}
					return options.MDMChanged(ctx)
				}
				return nil
			})
		}
	}
}
