// Package cliglobalconfig ports the CLI's shared .claude.json persistence.
// It is separate from the desktop recoverable-JSON service: neither corrupt
// files, locking, permissions, backups nor cache behavior are interchangeable.
package cliglobalconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/unicode/norm"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Updater func(*protocol.JSONObject) (*protocol.JSONObject, error)
type FileStat struct {
	Mtime float64
	Size  int64
	Mode  fs.FileMode
}
type FileSystem interface {
	ReadFile(string) ([]byte, error)
	Stat(string) (FileStat, error)
	Exists(string) bool
	MkdirAll(string) error
	Mkdir(string) error
	Remove(string) error
	ReadDir(string) ([]string, error)
	CopyFile(string, string) error
	Readlink(string) (string, error)
	Realpath(string) (string, error)
	Chmod(string, fs.FileMode) error
	Chtimes(string, time.Time) error
	Rename(string, string) error
	WriteFlush(string, []byte, *fs.FileMode) error
}
type Watcher func(path string, interval time.Duration, callback func(FileStat)) func()
type LeaseTimer interface {
	Channel() <-chan time.Time
	Stop() bool
}
type Options struct {
	Home string
	// nil means CLAUDE_CONFIG_DIR was absent. An explicitly empty value remains
	// distinct, matching the Source's || versus ?? path selection.
	ConfigDir       *string
	OAuthFileSuffix string
	Now             func() time.Time
	PID             int
	FS              FileSystem
	Watcher         Watcher
	Stderr          func(string)
	Diagnostic      func(kind string, value map[string]any)
	Diagnostics     *diagnostics.Process
	NowMillis       func() float64
	// Timer is a clock capability for proper-lockfile's scheduled lease update.
	// Save is synchronous; timer callbacks never renew inside that frame.
	Timer func(time.Duration) LeaseTimer
}
type Service struct {
	options          Options
	pathOnce         sync.Once
	path, configHome string
	stateMu          sync.Mutex
	loadMu           sync.Mutex
	saveMu           sync.Mutex
	enabled          bool
	cache            *protocol.JSONObject
	cacheMtime       float64
	lastRead         *FileStat
	watcherStarted   bool
	stopWatcher      func()
	closed           bool
	insideRead       bool
	precision        string
}

func New(options Options) (*Service, error) {
	if options.Home == "" || !filepath.IsAbs(options.Home) {
		return nil, errors.New("CLI global configuration requires an explicit absolute HOME")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.PID == 0 {
		options.PID = os.Getpid()
	}
	if options.FS == nil {
		options.FS = OSFileSystem{}
	}
	if options.Timer == nil {
		options.Timer = func(delay time.Duration) LeaseTimer { return realLeaseTimer{time.NewTimer(delay)} }
	}
	if options.Stderr == nil {
		options.Stderr = func(value string) { fmt.Fprint(os.Stderr, value) }
	}
	if options.NowMillis == nil {
		// Date.now is independent of the injected calendar/new Date primitive.
		options.NowMillis = func() float64 { return float64(time.Now().UnixMilli()) }
	}
	service := &Service{options: options}
	service.configHome = filepath.Join(options.Home, ".claude")
	if options.ConfigDir != nil {
		service.configHome = *options.ConfigDir
	}
	service.configHome = norm.NFC.String(service.configHome)
	return service, nil
}
func (service *Service) Path() string {
	service.pathOnce.Do(func() {
		legacy := filepath.Join(service.configHome, ".config.json")
		if service.options.FS.Exists(legacy) {
			service.path = legacy
			return
		}
		root := service.options.Home
		if service.options.ConfigDir != nil && *service.options.ConfigDir != "" {
			root = *service.options.ConfigDir
		}
		service.path = filepath.Join(root, ".claude"+service.options.OAuthFileSuffix+".json")
	})
	return service.path
}
func (service *Service) Enable() error {
	service.stateMu.Lock()
	if service.enabled {
		service.stateMu.Unlock()
		return nil
	}

	start := service.options.NowMillis()
	if service.options.Diagnostics != nil {
		if err := service.options.Diagnostics.Log("info", "enable_configs_started", nil); err != nil {
			service.stateMu.Unlock()
			return err
		}
	}
	service.enabled = true
	service.stateMu.Unlock()
	if _, err := service.read(true); err != nil {
		return err
	}
	duration := service.options.NowMillis() - start
	if service.options.Diagnostics != nil {
		data := protocol.NewJSONObject(protocol.JSONField{Name: "duration_ms", Value: diagnostics.Number(duration)})
		return service.options.Diagnostics.Log("info", "enable_configs_completed", &data)
	}
	return nil
}
func (service *Service) Get() (*protocol.JSONObject, error) {
	service.stateMu.Lock()
	cached := service.cache
	service.stateMu.Unlock()
	if cached != nil {
		return cached, nil
	}
	service.loadMu.Lock()
	defer service.loadMu.Unlock()
	service.stateMu.Lock()
	cached = service.cache
	service.stateMu.Unlock()
	if cached != nil {
		return cached, nil
	}
	stat, statErr := service.options.FS.Stat(service.Path())
	config, err := service.read(false)
	if err != nil {
		// Source's startup catch performs a second uncached getConfig read.
		// Recovery filesystem failures can therefore retry before a later Get
		// establishes the memory cache and watcher.
		fallback, fallbackErr := service.read(false)
		if fallbackErr != nil {
			return nil, fallbackErr
		}
		migrateConfigFields(fallback)
		return fallback, nil
	}
	migrateConfigFields(config)
	service.stateMu.Lock()
	service.cache = config
	service.cacheMtime = float64(service.options.Now().UnixMilli())
	if statErr == nil {
		service.cacheMtime = stat.Mtime
		service.lastRead = &stat
	}
	service.stateMu.Unlock()
	service.startWatcher()
	return config, nil
}
func (service *Service) emit(kind string, value map[string]any) {
	if service.options.Diagnostic != nil {
		service.options.Diagnostic(kind, value)
	}
}
func (service *Service) losesAuth(fresh *protocol.JSONObject) bool {
	service.stateMu.Lock()
	cached := service.cache
	service.stateMu.Unlock()
	if cached == nil {
		return false
	}
	return cached.Get("oauthAccount") != nil && fresh.Get("oauthAccount") == nil || bytes.Equal(cached.Get("hasCompletedOnboarding"), []byte("true")) && !bytes.Equal(fresh.Get("hasCompletedOnboarding"), []byte("true"))
}
func (service *Service) writeThrough(config *protocol.JSONObject) {
	service.stateMu.Lock()
	service.cache = config
	service.cacheMtime = float64(service.options.Now().UnixMilli())
	service.lastRead = nil
	service.stateMu.Unlock()
}
func (service *Service) Save(updater Updater) error {
	if updater == nil {
		return errors.New("CLI global config updater is required")
	}
	service.saveMu.Lock()
	defer service.saveMu.Unlock()
	merge := func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next, err := updater(current)
		if err != nil || next == current {
			return next, err
		}
		if next == nil {
			return nil, errors.New("CLI global config updater must return an ordered object")
		}
		written := next.Clone()
		projects, err := cleanProjectHistory(current.Get("projects"))
		if err != nil {
			return nil, err
		}
		written.Set("projects", projects)
		return &written, nil
	}
	return service.save(merge)
}
func (service *Service) save(merge Updater) error {
	written, didWrite, err := service.saveLocked(merge)
	if err == nil {
		if didWrite {
			service.writeThrough(written)
		}
		return nil
	}
	service.emit("lock_fallback", map[string]any{"error": err.Error()})
	current, err := service.read(false)
	if err != nil {
		return err
	}
	if service.losesAuth(current) {
		service.emit("auth_loss_prevented", nil)
		return nil
	}
	written, err = merge(current)
	if err != nil {
		return err
	}
	if written == current {
		return nil
	}
	if err = service.options.FS.MkdirAll(filepath.Dir(service.Path())); err != nil {
		return err
	}
	raw, err := filterDefaults(written)
	if err != nil {
		return err
	}
	mode := fs.FileMode(0600)
	if err = service.writeFile(service.Path(), raw, &mode); err != nil {
		return err
	}
	service.writeThrough(written)
	return nil
}
func (service *Service) saveLocked(merge Updater) (written *protocol.JSONObject, didWrite bool, err error) {
	if err = service.options.FS.MkdirAll(filepath.Dir(service.Path())); err != nil {
		return
	}
	start := service.options.Now().UnixMilli()
	release, err := service.acquireLock()
	if err != nil {
		return nil, false, err
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			err = releaseErr
		}
	}()
	if elapsed := service.options.Now().UnixMilli() - start; elapsed > 100 {
		service.emit("lock_contention", map[string]any{"lock_time_ms": elapsed})
	}
	service.stateMu.Lock()
	last := service.lastRead
	service.stateMu.Unlock()
	if last != nil {
		current, statErr := service.options.FS.Stat(service.Path())
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			return nil, false, statErr
		}
		if statErr == nil && (current.Mtime != last.Mtime || current.Size != last.Size) {
			service.emit("stale_write", map[string]any{"read_mtime": last.Mtime, "write_mtime": current.Mtime, "read_size": last.Size, "write_size": current.Size})
		}
	}
	current, err := service.read(false)
	if err != nil {
		return nil, false, err
	}
	if service.losesAuth(current) {
		service.emit("auth_loss_prevented", nil)
		return nil, false, nil
	}
	written, err = merge(current)
	if err != nil {
		return nil, false, err
	}
	if written == current {
		return nil, false, nil
	}
	raw, err := filterDefaults(written)
	if err != nil {
		return nil, false, err
	}
	service.backup()
	mode := fs.FileMode(0600)
	if err = service.writeFile(service.Path(), raw, &mode); err != nil {
		return nil, false, err
	}
	return written, true, nil
}
func (service *Service) RecordFirstStartTime() error {
	config, err := service.Get()
	if err != nil {
		return err
	}
	if jsTruthy(config.Get("firstStartTime")) {
		return nil
	}
	stamp, _ := json.Marshal(service.options.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	return service.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		raw := current.Get("firstStartTime")
		if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			raw = stamp
		}
		next.Set("firstStartTime", raw)
		return &next, nil
	})
}
func (service *Service) Close() error {
	service.stateMu.Lock()
	service.closed = true
	stop := service.stopWatcher
	service.stopWatcher = nil
	service.watcherStarted = false
	service.stateMu.Unlock()
	if stop != nil {
		stop()
	}
	return nil
}

func (service *Service) startWatcher() {
	service.stateMu.Lock()
	if service.watcherStarted || service.closed {
		service.stateMu.Unlock()
		return
	}
	service.watcherStarted = true
	service.stateMu.Unlock()
	watch := service.options.Watcher
	if watch == nil {
		watch = service.watchFile
	}
	stop := watch(service.Path(), time.Second, service.refresh)
	service.stateMu.Lock()
	if service.closed {
		service.stateMu.Unlock()
		stop()
		return
	}
	service.stopWatcher = stop
	service.stateMu.Unlock()
}
func (service *Service) watchFile(path string, interval time.Duration, callback func(FileStat)) func() {
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		poll := func() { stat, _ := service.options.FS.Stat(path); callback(stat) }
		poll()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				poll()
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}
func (service *Service) refresh(stat FileStat) {
	service.saveMu.Lock()
	service.stateMu.Lock()
	stale := stat.Mtime <= service.cacheMtime || service.closed
	service.stateMu.Unlock()
	service.saveMu.Unlock()
	if stale {
		return
	}
	raw, err := service.options.FS.ReadFile(service.Path())
	if err != nil {
		return
	}
	service.saveMu.Lock()
	defer service.saveMu.Unlock()
	service.stateMu.Lock()
	defer service.stateMu.Unlock()
	if stat.Mtime <= service.cacheMtime || service.closed {
		return
	}
	parsed, err := parseSpread(raw, true)
	if err != nil {
		return
	}
	migrateConfigFields(parsed)
	service.cache = parsed
	service.cacheMtime = stat.Mtime
	service.lastRead = &stat
}
