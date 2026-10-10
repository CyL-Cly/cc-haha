package skillwatcher

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var fixtureTiming = Timing{10 * time.Millisecond, 50 * time.Millisecond, 10 * time.Millisecond, 30 * time.Millisecond, 10 * time.Millisecond}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-timeout.C:
			t.Fatal("actual file watcher did not deliver command update")
		case <-tick.C:
		}
	}
}
func TestPhysicalFileStabilityAtomicReplacementUnlinkAndMissingRoot(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "config.json")
	_ = os.WriteFile(file, []byte(`{"enabled":false}`), 0600)
	var mu sync.Mutex
	calls := []string{}
	w, e := Start(Options{Paths: []string{file}, Timing: &fixtureTiming, Reload: func(ctx context.Context, path string, dynamic bool) error {
		mu.Lock()
		calls = append(calls, path)
		mu.Unlock()
		return nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(calls) }
	time.Sleep(80 * time.Millisecond)
	if count() != 0 {
		t.Fatal("ignoreInitial emitted")
	}
	for i := 0; i < 5; i++ {
		_ = os.WriteFile(file, []byte(`{"enabled":true,"write":`+string(rune('0'+i))+`}`), 0600)
		time.Sleep(12 * time.Millisecond)
	}
	waitFor(t, func() bool { return count() == 1 })
	time.Sleep(80 * time.Millisecond)
	if count() != 1 {
		t.Fatal("unstable writes were not batched")
	}
	temporary := filepath.Join(root, "swap")
	_ = os.WriteFile(temporary, []byte(`{"enabled":false}`), 0600)
	_ = os.Rename(temporary, file)
	waitFor(t, func() bool { return count() == 2 })
	_ = os.Remove(file)
	waitFor(t, func() bool { return count() == 3 })
	_ = os.WriteFile(file, []byte(`{"enabled":true}`), 0600)
	waitFor(t, func() bool { return count() == 4 })
	w.Close()
	_ = os.WriteFile(file, []byte(`{"enabled":false}`), 0600)
	time.Sleep(120 * time.Millisecond)
	if count() != 4 {
		t.Fatal("disposed watcher emitted")
	}
	missing := filepath.Join(root, "never-existed.json")
	w2, e := Start(Options{Paths: []string{missing}, Timing: &fixtureTiming, Reload: func(context.Context, string, bool) error {
		t.Error("missing-at-initialize path was watched")
		return nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer w2.Close()
	_ = os.WriteFile(missing, []byte(`{}`), 0600)
	time.Sleep(120 * time.Millisecond)
}
func TestCloseCancelsAndDrainsActualReload(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config")
	_ = os.WriteFile(file, []byte("before"), 0600)
	started, drained := make(chan struct{}), make(chan struct{})
	w, e := Start(Options{Paths: []string{file}, Timing: &fixtureTiming, Reload: func(ctx context.Context, _ string, _ bool) error {
		close(started)
		<-ctx.Done()
		close(drained)
		return ctx.Err()
	}})
	if e != nil {
		t.Fatal(e)
	}
	_ = os.WriteFile(file, []byte("after"), 0600)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("reload not admitted")
	}
	w.Close()
	select {
	case <-drained:
	default:
		t.Fatal("Close returned before consumer drained")
	}
}
func TestDepthGitAndSpecialFilesRemainOutsideWatchSet(t *testing.T) {
	root := t.TempDir()
	paths := []string{filepath.Join(root, ".git", "config"), filepath.Join(root, "one", "two", "three", "SKILL.md")}
	for _, path := range paths {
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		_ = os.WriteFile(path, []byte("before"), 0600)
	}
	w, e := Start(Options{Paths: []string{root}, Timing: &fixtureTiming, Reload: func(context.Context, string, bool) error { t.Error("ignored path emitted"); return nil }})
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	for _, path := range paths {
		_ = os.WriteFile(path, []byte("after"), 0600)
	}
	time.Sleep(150 * time.Millisecond)
}
