package taskstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func taskInput(s string) protocol.JSONObject {
	var object protocol.JSONObject
	if err := object.UnmarshalJSON([]byte(s)); err != nil {
		panic(err)
	}
	return object
}
func testStore(t *testing.T, root string) *Service {
	t.Helper()
	s, e := New(Options{ConfigDir: root, PID: os.Getpid(), Now: time.Now})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func baseTask(subject string) protocol.JSONObject {
	return taskInput(fmt.Sprintf(`{"subject":%q,"description":"details","status":"pending","blocks":[],"blockedBy":[]}`, subject))
}
func TestConcurrentStoresSerializeIDsMetadataAndSnapshots(t *testing.T) {
	root := t.TempDir()
	a, b := testStore(t, root), testStore(t, root)
	var wg sync.WaitGroup
	failures := make(chan error, 30)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			result, e := s.Create("parallel", baseTask(strconv.Itoa(i)))
			if e == nil && result.Revision < 1 {
				e = errors.New("missing actual revision")
			}
			failures <- e
		}(i)
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	snapshot, e := a.Snapshot("parallel")
	if e != nil {
		t.Fatal(e)
	}
	if len(snapshot.Tasks) != 20 || snapshot.Revision != 20 {
		t.Fatalf("snapshot %#v", snapshot)
	}
	ids := map[string]bool{}
	for _, task := range snapshot.Tasks {
		ids[task.String("id")] = true
	}
	for i := 1; i <= 20; i++ {
		if !ids[strconv.Itoa(i)] {
			t.Fatalf("ID %d reused/lost", i)
		}
	}
	failures = make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			_, e := s.Update("parallel", quote("1"), taskInput(fmt.Sprintf(`{"metadata":{"key%d":%d}}`, i, i)))
			failures <- e
		}(i)
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	first, e := a.Get("parallel", quote("1"))
	if e != nil || first == nil {
		t.Fatal(e)
	}
	var metadata protocol.JSONObject
	_ = metadata.UnmarshalJSON(first.Get("metadata"))
	if len(metadata.OwnProperties()) != 12 {
		t.Fatalf("concurrent metadata lost %s", raw(metadata))
	}
	noChange, e := a.Update("parallel", quote("1"), taskInput(`{"metadata":{"key0":0}}`))
	if e != nil || noChange.Updated || noChange.Revision != 32 {
		t.Fatalf("no-op changed commit %#v %v", noChange, e)
	}
	assertNoLeases(t, root)
}
func assertNoLeases(t *testing.T, root string) {
	t.Helper()
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			t.Error(err)
			return nil
		}
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".lock") {
			t.Errorf("leaked mkdir lease %s", path)
		}
		return nil
	})
}
func TestTaskStoreProcessHelper(t *testing.T) {
	root := os.Getenv("CC_HAHA_TASKSTORE_TEST_PROCESS")
	if root == "" {
		return
	}
	s := testStore(t, root)
	for i := 0; i < 5; i++ {
		if _, e := s.Create("process", baseTask(strconv.Itoa(i))); e != nil {
			t.Fatal(e)
		}
	}
}
func TestSeparateProcessesUseSameDurableLockAndRevision(t *testing.T) {
	root := t.TempDir()
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(executable, "-test.run=^TestTaskStoreProcessHelper$")
			cmd.Env = []string{"HOME=" + root, "USERPROFILE=" + root, "CLAUDE_CONFIG_DIR=" + root, "CC_HAHA_TASKSTORE_TEST_PROCESS=" + root, "PATH=/usr/bin:/bin", "TMPDIR=" + root}
			output, e := cmd.CombinedOutput()
			if e != nil {
				e = fmt.Errorf("child %w: %s", e, output)
			}
			failures <- e
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	snapshot, e := testStore(t, root).Snapshot("process")
	if e != nil || len(snapshot.Tasks) != 20 || snapshot.Revision != 20 {
		t.Fatalf("process transaction failed %#v %v", snapshot, e)
	}
	assertNoLeases(t, root)
}
func TestQueuedWriterCannotJoinNewLifecycle(t *testing.T) {
	s := testStore(t, t.TempDir())
	target := s.lifecyclePath("queued", ".lock")
	if e := ensureLockTarget(target); e != nil {
		t.Fatal(e)
	}
	release, e := s.lock(target)
	if e != nil {
		t.Fatal(e)
	}
	result := make(chan error, 1)
	// Capture exactly the pre-lock identity through the same writer primitive;
	// the lock is held by the old generation while the actual writer queues.
	expected, e := s.readLifecycle("queued")
	if e != nil {
		t.Fatal(e)
	}
	wrote := false
	go func() {
		result <- s.withLifecycleGeneration("queued", &expected, func(lifecycle) error { wrote = true; return nil })
	}()
	state := `{"schemaVersion":1,"generation":1,"deleted":false,"activeIdentity":{"teamName":"new","createdAt":2},"terminals":[]}`
	if e := os.WriteFile(s.lifecyclePath("queued", ".state.json"), []byte(state), 0666); e != nil {
		t.Fatal(e)
	}
	if e := release(); e != nil {
		t.Fatal(e)
	}
	e = <-result
	if e == nil || e.Error() != "Task list queued lifecycle changed while waiting for the lock" {
		t.Fatalf("queued writer crossed generation: %v", e)
	}
	if wrote {
		t.Fatal("queued old-generation transaction executed")
	}
	files, _ := os.ReadDir(s.tasksDir("queued"))
	if len(files) > 0 {
		t.Fatalf("stale task file produced: %v", files)
	}
	assertNoLeases(t, s.options.ConfigDir)
}
func TestStaleLeaseRemovalAndInvalidRevisionKeepSourceBoundary(t *testing.T) {
	s := testStore(t, t.TempDir())
	target := filepath.Join(s.options.ConfigDir, "stale")
	if e := ensureLockTarget(target); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(target+".lock", 0777); e != nil {
		t.Fatal(e)
	}
	stale := time.Now().Add(-20 * time.Second)
	if e := os.Chtimes(target+".lock", stale, stale); e != nil {
		t.Fatal(e)
	}
	release, e := s.lock(target)
	if e != nil {
		t.Fatal(e)
	}
	if e := release(); e != nil {
		t.Fatal(e)
	}
	for _, value := range []string{"", "-1", "1.1", "9007199254740992", "NaN", "1junk"} {
		list := "invalid"
		if e := os.MkdirAll(s.tasksDir(list), 0777); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(s.tasksDir(list), ".revision"), []byte(value), 0666); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Create(list, baseTask("not written")); e == nil {
			t.Fatalf("invalid revision %q silently reset", value)
		}
		if task, _ := s.Get(list, quote("1")); task != nil {
			t.Fatal("revision failure still wrote task")
		}
	}
	assertNoLeases(t, s.options.ConfigDir)
}
func TestUTF16IDsLegacyStatusesAndSchemaStripping(t *testing.T) {
	s := testStore(t, t.TempDir())
	if SanitizePathComponent(" list/😀 ") != "-list----" {
		t.Fatal("Source UTF16 sanitization changed")
	}
	if e := os.MkdirAll(s.tasksDir("codec"), 0777); e != nil {
		t.Fatal(e)
	}
	path, e := s.taskPath("codec", json.RawMessage(`"\ud800"`))
	if e != nil || filepath.Base(path) != "-.json" {
		t.Fatalf("surrogate filename %s %v", path, e)
	}
	data := `{"id":"\ud800","subject":"\ud800","description":"d","status":"open","blocks":[],"blockedBy":[],"metadata":{"__proto__":3,"\ud800":"value"},"future":1}`
	if e := os.WriteFile(path, []byte(data), 0666); e != nil {
		t.Fatal(e)
	}
	if task, _ := s.Get("codec", json.RawMessage(`"\ud800"`)); task != nil {
		t.Fatal("external Source accepted ant-only legacy status")
	}
	s.options.UserType = "ant"
	task, e := s.Get("codec", json.RawMessage(`"\ud800"`))
	if e != nil || task == nil || task.String("status") != "pending" || task.Get("future") != nil {
		t.Fatalf("legacy read %v %v", task, e)
	}
	var metadata protocol.JSONObject
	_ = metadata.UnmarshalJSON(task.Get("metadata"))
	if metadata.Get("__proto__") != nil {
		t.Fatal("Zod record polluted prototype key")
	}
}
