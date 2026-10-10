package settingswatch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var physicalTiming = Timing{60 * time.Millisecond, 10 * time.Millisecond, 170 * time.Millisecond, 100 * time.Millisecond, time.Hour}

func waitSettings(t *testing.T, condition func() bool) {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	for time.Now().Before(end) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("actual settings watcher did not settle")
}
func writeSettings(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCollectExistingDirectoriesPotentialSiblingsFlagsAndDropIns(t *testing.T) {
	root := t.TempDir()
	user := filepath.Join(root, "config", "settings.json")
	project := filepath.Join(root, "project", ".claude", "settings.json")
	local := filepath.Join(filepath.Dir(project), "settings.local.json")
	flag := filepath.Join(root, "flags", "settings.json")
	drop := filepath.Join(root, "managed", "managed-settings.d")
	writeSettings(t, user, `{}`)
	writeSettings(t, project, `{}`)
	writeSettings(t, flag, `{}`)
	if err := os.MkdirAll(drop, 0700); err != nil {
		t.Fatal(err)
	}
	files := []SourceFile{{"userSettings", user}, {"projectSettings", project}, {"localSettings", local}, {"flagSettings", flag}, {"policySettings", filepath.Join(root, "managed", "managed-settings.json")}}
	targets, err := Collect(files, drop)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(targets.Directories, []string{filepath.Dir(user), filepath.Dir(project), drop}) || !reflect.DeepEqual(targets.Files, files[:3]) {
		t.Fatalf("actual target policy %#v", targets)
	}
	if SourceForPath(filepath.Join(drop, "nested", "file.json"), files, drop) != "policySettings" {
		t.Fatal("Source path classifier prefix changed")
	}
	if targets.accepts(filepath.Join(drop, "nested", "file.json")) || targets.accepts(filepath.Join(drop, "wrong.txt")) || targets.accepts(flag) || !targets.accepts(local) || !targets.accepts(filepath.Join(drop, ".hidden.json")) {
		t.Fatal("depth/file/flag watch filter changed")
	}
}

func TestPhysicalSettingsChangesStabilityInternalEchoSiblingsAtomicDeleteAndCleanup(t *testing.T) {
	root := t.TempDir()
	user := filepath.Join(root, "settings.json")
	local := filepath.Join(root, "settings.local.json")
	drop := filepath.Join(root, "managed-settings.d")
	if err := os.MkdirAll(drop, 0700); err != nil {
		t.Fatal(err)
	}
	writeSettings(t, user, `{"before":true}`)
	var mu sync.Mutex
	events := []SourceFile{}
	marked := map[string]bool{}
	cleared := false
	watcher, err := Start(Options{Files: []SourceFile{{"userSettings", user}, {"localSettings", local}}, DropInDirectory: drop, Timing: &physicalTiming, Change: func(ctx context.Context, source, path string) error {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, SourceFile{source, path})
		return nil
	}, ConsumeInternalWrite: func(path string) bool {
		mu.Lock()
		defer mu.Unlock()
		if marked[path] {
			delete(marked, path)
			return true
		}
		return false
	}, ClearInternalWrites: func() { mu.Lock(); defer mu.Unlock(); cleared = true }})
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(events) }
	time.Sleep(80 * time.Millisecond)
	if count() != 0 {
		t.Fatal("ignoreInitial emitted settings")
	}
	mu.Lock()
	marked[user] = true
	mu.Unlock()
	writeSettings(t, user, `{"own":true}`)
	time.Sleep(150 * time.Millisecond)
	if count() != 0 {
		t.Fatal("internal write echo triggered ConfigChange")
	}
	for i := 0; i < 4; i++ {
		writeSettings(t, user, string(make([]byte, 10+i)))
		time.Sleep(15 * time.Millisecond)
	}
	waitSettings(t, func() bool { return count() == 1 })
	time.Sleep(100 * time.Millisecond)
	if count() != 1 {
		t.Fatal("unstable write emitted repeatedly")
	}
	writeSettings(t, local, `{"new":true}`)
	waitSettings(t, func() bool { return count() == 2 })
	writeSettings(t, filepath.Join(root, "ignored.json"), `{}`)
	writeSettings(t, filepath.Join(drop, "nested", "ignored.json"), `{}`)
	time.Sleep(150 * time.Millisecond)
	if count() != 2 {
		t.Fatal("unknown/deep file triggered settings change")
	}
	fragment := filepath.Join(drop, "fragment.json")
	writeSettings(t, fragment, `{}`)
	waitSettings(t, func() bool { return count() == 3 })
	if err := os.Remove(user); err != nil {
		t.Fatal(err)
	}
	time.Sleep(35 * time.Millisecond)
	writeSettings(t, user, `{"recreated":true}`)
	waitSettings(t, func() bool { return count() == 4 })
	time.Sleep(350 * time.Millisecond)
	if count() != 4 {
		t.Fatal("delete/recreate did not cancel deletion grace")
	}
	if err := os.Remove(user); err != nil {
		t.Fatal(err)
	}
	waitSettings(t, func() bool { return count() == 5 })
	watcher.Close()
	mu.Lock()
	wasCleared := cleared
	mu.Unlock()
	if !wasCleared {
		t.Fatal("Close did not clear actual internal marks")
	}
	writeSettings(t, user, `{"after-close":true}`)
	time.Sleep(150 * time.Millisecond)
	if count() != 5 {
		t.Fatal("settings callback survived Close")
	}
}

func TestMDMPollOnlyChangedSnapshotAndPhysicalCancellationJoin(t *testing.T) {
	timing := physicalTiming
	timing.MDMPoll = 30 * time.Millisecond
	var updates, reads atomic.Int64
	var mu sync.Mutex
	snapshot := json.RawMessage(`{"mdm":{}}`)
	started, drained := make(chan struct{}), make(chan struct{})
	watcher, err := Start(Options{Timing: &timing, Change: func(context.Context, string, string) error {
		t.Error("MDM poll ran a file ConfigChange Hook")
		return nil
	}, MDM: func(ctx context.Context, refresh bool) (json.RawMessage, error) {
		if !refresh {
			return append([]byte{}, snapshot...), nil
		}
		reads.Add(1)
		mu.Lock()
		value := append([]byte{}, snapshot...)
		mu.Unlock()
		return value, nil
	}, MDMChanged: func(context.Context) error { updates.Add(1); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	waitSettings(t, func() bool { return reads.Load() > 1 })
	if updates.Load() != 0 {
		t.Fatal("unchanged MDM snapshot emitted")
	}
	mu.Lock()
	snapshot = []byte(`{"mdm":{"policy":true}}`)
	mu.Unlock()
	waitSettings(t, func() bool { return updates.Load() == 1 })
	time.Sleep(100 * time.Millisecond)
	if updates.Load() != 1 {
		t.Fatal("same MDM snapshot emitted repeatedly")
	}
	watcher.Close()
	var once, drainOnce sync.Once
	watcher, err = Start(Options{Timing: &timing, Change: func(context.Context, string, string) error { return nil }, MDM: func(ctx context.Context, refresh bool) (json.RawMessage, error) {
		if !refresh {
			return []byte(`{}`), nil
		}
		once.Do(func() { close(started) })
		<-ctx.Done()
		drainOnce.Do(func() { close(drained) })
		return nil, ctx.Err()
	}, MDMChanged: func(context.Context) error { t.Error("cancelled MDM refresh applied"); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("native MDM primitive not started")
	}
	watcher.Close()
	select {
	case <-drained:
	default:
		t.Fatal("Close returned before native MDM join")
	}
}

func TestRemoteSkipsFilesystemAndMDMGetters(t *testing.T) {
	watcher, err := Start(Options{Remote: true, Files: []SourceFile{{"userSettings", "relative-invalid"}}, Change: func(context.Context, string, string) error { t.Error("remote executed settings change"); return nil }, MDM: func(context.Context, bool) (json.RawMessage, error) { t.Error("remote read MDM"); return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	watcher.Close()
}
