package cliglobalconfig

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func privateService(t *testing.T, options Options) *Service {
	t.Helper()
	if options.Home == "" {
		options.Home = t.TempDir()
	}
	if options.Watcher == nil {
		options.Watcher = func(string, time.Duration, func(FileStat)) func() { return func() {} }
	}
	if options.Stderr == nil {
		options.Stderr = func(string) {}
	}
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}
func seedConfig(t *testing.T, service *Service, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(service.Path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(service.Path(), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := service.options.Now().Add(-time.Second)
	if err := os.Chtimes(service.Path(), stamp, stamp); err != nil {
		t.Fatal(err)
	}
}
func enabledService(t *testing.T, options Options, raw string) *Service {
	t.Helper()
	service := privateService(t, options)
	seedConfig(t, service, raw)
	if err := service.Enable(); err != nil {
		t.Fatal(err)
	}
	return service
}
func patchFixture(current *protocol.JSONObject) (*protocol.JSONObject, error) {
	next := current.Clone()
	next.Set("native", []byte("true"))
	return &next, nil
}

func TestExplicitHostPathsAndUpdaterBoundary(t *testing.T) {
	for _, home := range []string{"", "relative"} {
		if _, err := New(Options{Home: home}); err == nil {
			t.Fatal("implicit/relative HOME accepted")
		}
	}
	service := privateService(t, Options{OAuthFileSuffix: "-fake"})
	if service.Path() != filepath.Join(service.options.Home, ".claude-fake.json") {
		t.Fatal(service.Path())
	}
	if err := service.RecordFirstStartTime(); err == nil || !strings.Contains(err.Error(), "before allowed") {
		t.Fatal(err)
	}
	if err := service.Save(nil); err == nil {
		t.Fatal("nil updater accepted")
	}
	seedConfig(t, service, "{}")
	if err := service.Enable(); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := service.Save(func(*protocol.JSONObject) (*protocol.JSONObject, error) { calls++; return nil, nil }); err == nil || calls != 2 {
		t.Fatalf("nil updater return lost actual locked/fallback ordering: %v calls=%d", err, calls)
	}
	for _, key := range []string{""} {
		if _, err := service.GetProject(key); err == nil {
			t.Fatal("missing resolved key accepted")
		}
		if err := service.SaveProject(key, patchFixture); err == nil {
			t.Fatal("missing project key accepted")
		}
	}
	if err := service.SaveProject("/fixture", nil); err == nil {
		t.Fatal("nil project updater accepted")
	}
	if err := service.SaveProject("/fixture", func(*protocol.JSONObject) (*protocol.JSONObject, error) { return nil, nil }); !errors.Is(err, ErrProjectShape) {
		t.Fatal(err)
	}
	if err := service.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		next.Set("invalid", json.RawMessage("{broken"))
		return &next, nil
	}); err == nil {
		t.Fatal("malformed raw updater JSON written")
	}
	raw, _ := os.ReadFile(service.Path())
	if string(raw) != "{}" {
		t.Fatal("invalid update changed protected config", string(raw))
	}
}

type readCapability struct {
	OSFileSystem
	read func(string) ([]byte, error)
}

func (capability readCapability) ReadFile(path string) ([]byte, error) {
	if capability.read != nil {
		return capability.read(path)
	}
	return capability.OSFileSystem.ReadFile(path)
}

func TestConcurrentStartupKeepsOneCacheReference(t *testing.T) {
	start, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	capability := readCapability{read: func(path string) ([]byte, error) {
		if reads.Add(1) == 2 {
			close(start)
			<-release
		}
		return os.ReadFile(path)
	}}
	service := enabledService(t, Options{FS: capability}, `{"future":true}`)
	const workers = 16
	results := make(chan *protocol.JSONObject, workers)
	failures := make(chan error, workers)
	var ready sync.WaitGroup
	ready.Add(workers)
	for range workers {
		go func() {
			ready.Done()
			value, err := service.Get()
			results <- value
			failures <- err
		}()
	}
	<-start
	ready.Wait()
	close(release)
	first := <-results
	for range workers - 1 {
		if <-results != first {
			t.Fatal("parallel startup returned distinct cache references")
		}
	}
	for range workers {
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
	}
	if reads.Load() != 2 {
		t.Fatal("cache misses caused repeated sync reads", reads.Load())
	}
}

func TestWatchReadDoesNotRegressWriteThroughAndClose(t *testing.T) {
	for _, closeDuringRead := range []bool{false, true} {
		t.Run(map[bool]string{false: "write-through", true: "close"}[closeDuringRead], func(t *testing.T) {
			started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var block atomic.Bool
			capability := readCapability{read: func(path string) ([]byte, error) {
				raw, err := os.ReadFile(path)
				if block.Swap(false) {
					close(started)
					<-release
				}
				return raw, err
			}}
			var now atomic.Int64
			now.Store(946684800000)
			service := enabledService(t, Options{FS: capability, Now: func() time.Time { return time.UnixMilli(now.Load()) }}, `{"future":"old"}`)
			before, err := service.Get()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(service.Path(), []byte(`{"future":"stale-watch"}`), 0600); err != nil {
				t.Fatal(err)
			}
			block.Store(true)
			go func() { service.refresh(FileStat{Mtime: 946684802000}); close(finished) }()
			<-started
			if closeDuringRead {
				_ = service.Close()
			} else {
				now.Store(946684803000)
				if err := service.Save(patchFixture); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			<-finished
			after, err := service.Get()
			if err != nil {
				t.Fatal(err)
			}
			if closeDuringRead && after != before || !closeDuringRead && string(after.Get("native")) != "true" {
				t.Fatal("late watcher replaced close/write-through snapshot", fixtureString(after))
			}
			service.refresh(FileStat{}) // initial/deleted stat is ignored.
		})
	}
}

func TestWatchReadFailureRetainsSnapshotAndNativePollStops(t *testing.T) {
	var failing atomic.Bool
	capability := readCapability{read: func(path string) ([]byte, error) {
		if failing.Load() {
			return nil, fs.ErrPermission
		}
		return os.ReadFile(path)
	}}
	service := enabledService(t, Options{FS: capability}, `{"future":"retained"}`)
	before, _ := service.Get()
	failing.Store(true)
	service.refresh(FileStat{Mtime: float64(time.Now().Add(time.Hour).UnixMilli())})
	after, _ := service.Get()
	if after != before {
		t.Fatal("read failure replaced cache")
	}
	failing.Store(false)
	polled := make(chan FileStat, 4)
	stop := service.watchFile(service.Path(), 10*time.Millisecond, func(stat FileStat) { polled <- stat })
	for range 2 {
		select {
		case stat := <-polled:
			if stat.Size == 0 {
				t.Fatal("native polling did not stat real private config")
			}
		case <-time.After(time.Second):
			t.Fatal("native polling failed")
		}
	}
	stop()
	stop()
	for len(polled) > 0 {
		<-polled
	}
	select {
	case <-polled:
		t.Fatal("polling continued after stop")
	case <-time.After(30 * time.Millisecond):
	}
	_ = service.Close()
	service.startWatcher()
}

func TestWatcherCloseDuringInstallation(t *testing.T) {
	var service *Service
	var stopped atomic.Bool
	service = enabledService(t, Options{Watcher: func(string, time.Duration, func(FileStat)) func() {
		_ = service.Close()
		return func() { stopped.Store(true) }
	}}, "{}")
	if _, err := service.Get(); err != nil {
		t.Fatal(err)
	}
	if !stopped.Load() {
		t.Fatal("watcher registration survived concurrent close")
	}
	service.startWatcher()
}

func TestOSWriterErrorsPreserveSourceAndModes(t *testing.T) {
	filesystem := OSFileSystem{}
	home := t.TempDir()
	source := filepath.Join(home, "source")
	dir := filepath.Join(home, "directory")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("protected"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.WriteFlush(dir, []byte("bad"), nil); err == nil {
		t.Fatal("directory writer succeeded")
	}
	if err := filesystem.CopyFile(filepath.Join(home, "absent"), filepath.Join(home, "out")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	if err := filesystem.CopyFile(source, dir); err == nil {
		t.Fatal("copy overwrote directory")
	}
	target := filepath.Join(home, "copy")
	if err := filesystem.CopyFile(source, target); err != nil {
		t.Fatal(err)
	}
	stat, err := filesystem.Stat(target)
	if err != nil || stat.Mode.Perm() != 0640 {
		t.Fatalf("copy mode: %v %v", stat, err)
	}
	if _, err := filesystem.ReadDir(filepath.Join(home, "absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestFixtureGuardRejectsHomeAndConfigSymlinksBeforeService(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CC_HAHA_GLOBAL_CONFIG_FIXTURE", "1")
	t.Setenv("CC_HAHA_FIXTURE_TEMP_ROOT", root)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("NODE_ENV", "production")
	t.Setenv("BUN_OPTIONS", "--no-env-file")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Chdir(home)
	if err := fixtureGuard(fixtureInput{Home: home}); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "private-sentinel")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}
	if err := fixtureGuard(fixtureInput{Home: home}); err == nil {
		t.Fatal("fixture config symlink accepted")
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	if err := fixtureGuard(fixtureInput{Home: alias}); err == nil {
		t.Fatal("fixture HOME symlink accepted")
	}
}
