package errorlog

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDeferredBatchesCoalesceAndCloseDrainsWithoutAdditionalCallbackWrites(t *testing.T) {
	root := t.TempDir()
	deferred := []func(){}
	env := map[string]string{"USER_TYPE": "ant", "NODE_ENV": "test", "CLAUDE_CONFIG_DIR": root}
	now := func() time.Time { return time.UnixMilli(1700000000000) }
	s, e := NewNativeSink(NativeSinkOptions{Files: physicalSinkFiles{}, Now: now, Metadata: func() Metadata {
		return Metadata{Environment: env, CWD: "/private-owned/project", ConfigDir: root, Platform: "darwin", Version: "actual", SessionID: "actual"}
	}, Defer: func(f func()) { deferred = append(deferred, f) }})
	if e != nil {
		t.Fatal(e)
	}
	for i := range 151 {
		s.Write(Error{Message: stringInt(i)})
	}
	if len(sinkSnapshot(t, root)) != 0 || len(deferred) != 1 {
		t.Fatalf("overflow wrote synchronously or did not coalesce: files%v pending%d", sinkSnapshot(t, root), len(deferred))
	}
	s.Close()
	first := sinkSnapshot(t, root)
	for _, f := range deferred {
		f()
	}
	s.Close()
	s.Write(errors.New("closed"))
	after := sinkSnapshot(t, root)
	if len(first) != 1 || len(after) != 1 {
		t.Fatal(first, after)
	}
	for path, text := range first {
		if text != after[path] || len(strings.Split(strings.TrimSpace(text), "\n")) != 151 {
			t.Fatal("close lost/duplicated buffered rows")
		}
	}
}

type failingLogFiles struct {
	mu       sync.Mutex
	attempts int
}

func (f *failingLogFiles) MkdirAll(string) error { return errors.New("private mkdir failure") }
func (f *failingLogFiles) AppendFile(string, []byte) error {
	f.mu.Lock()
	f.attempts++
	f.mu.Unlock()
	return errors.New("private append failure")
}
func (f *failingLogFiles) Remove(string) error          { return nil }
func (f *failingLogFiles) Symlink(string, string) error { return nil }
func TestEnabledSinkIOFailureDoesNotChangeRecoverableErrorReporting(t *testing.T) {
	files := &failingLogFiles{}
	env := map[string]string{"DEBUG": "true"}
	now := func() time.Time { return time.UnixMilli(1700000000000) }
	// Explicit debug startup has the same append failure as Source initSinks.
	_, e := NewNativeSink(NativeSinkOptions{Files: files, Now: now, Metadata: func() Metadata {
		return Metadata{Environment: env, ConfigDir: filepath.Join(t.TempDir(), "debug"), SessionID: "private"}
	}})
	if e == nil {
		t.Fatal("explicit debug startup append error lost")
	}
	env = map[string]string{"USER_TYPE": "ant", "NODE_ENV": "test", "CLAUDE_CONFIG_DIR": t.TempDir()}
	sink, e := NewNativeSink(NativeSinkOptions{Files: files, Now: now, Metadata: func() Metadata {
		return Metadata{Environment: env, CWD: "private", ConfigDir: env["CLAUDE_CONFIG_DIR"]}
	}, Defer: func(func()) {}})
	if e != nil {
		t.Fatal(e)
	}
	service, _ := New(Options{Environment: func() map[string]string { return env }, Now: now})
	service.Attach(sink.Write)
	service.Log(errors.New("retained original"))
	sink.Close()
	if len(service.Memory()) != 1 || service.Memory()[0].Error != "retained original" {
		t.Fatal("file failure changed memory/original error")
	}
}
func TestSinkRetainsOriginalErrorIdentityAndFirstSink(t *testing.T) {
	service, _ := New(Options{Environment: func() map[string]string { return map[string]string{} }, Now: time.Now})
	original := &Error{Name: "Error", Message: "before attachment"}
	service.Log(original)
	original.Message = "changed before drain"
	called := 0
	service.Attach(func(e error) {
		called++
		if e != original {
			t.Fatal("original error identity lost")
		}
	})
	service.Attach(func(error) { t.Fatal("replaced original sink") })
	if called != 1 || service.Memory()[0].Error != "before attachment" {
		t.Fatal("queue/memory timing changed")
	}
}

type observedLogFiles struct {
	physicalSinkFiles
	once     sync.Once
	appended chan struct{}
}

func (f *observedLogFiles) AppendFile(path string, data []byte) error {
	e := f.physicalSinkFiles.AppendFile(path, data)
	if e == nil {
		f.once.Do(func() { close(f.appended) })
	}
	return e
}
func TestNativeSinkActualTimerFlushAndCloseAreJoined(t *testing.T) {
	root := t.TempDir()
	files := &observedLogFiles{appended: make(chan struct{})}
	env := map[string]string{"USER_TYPE": "ant", "NODE_ENV": "test", "CLAUDE_CONFIG_DIR": root}
	sink, e := NewNativeSink(NativeSinkOptions{Files: files, Now: time.Now, Metadata: func() Metadata { return Metadata{Environment: env, CWD: "owned-timer", SessionID: "private"} }})
	if e != nil {
		t.Fatal(e)
	}
	defer sink.Close()
	sink.Write(errors.New("timer-delivered actual row"))
	select {
	case <-files.appended:
	case <-time.After(10 * time.Second):
		t.Fatal("actual native one-second timer did not flush")
	}
	sink.Close()
	filesBefore := sinkSnapshot(t, root)
	sink.Close()
	filesAfter := sinkSnapshot(t, root)
	if len(filesBefore) != 1 || len(filesAfter) != 1 {
		t.Fatal(filesBefore, filesAfter)
	}
	for path, text := range filesBefore {
		if text != filesAfter[path] || !strings.Contains(text, "timer-delivered actual row") {
			t.Fatal("timer write lost or close duplicated it")
		}
	}
}
