package taskstore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func seedDesktopTasks(t *testing.T, config string) {
	t.Helper()
	files := map[string]string{
		"numeric/10.json":             `{"id":"10","subject":"ten","description":"details","status":"completed","blocks":[],"blockedBy":[],"metadata":{"future":{"ok":true}}}`,
		"numeric/2.json":              `{"id":2,"subject":"two","status":"resolved","blocks":"legacy","blockedBy":[1,"x",null],"owner":null,"activeForm":false}`,
		"numeric/1.json":              `{"id":"1","subject":42,"description":false,"metadata":null}`,
		"numeric/internal.json":       `{"id":"3","subject":"internal","metadata":{"_internal":true}}`,
		"numeric/falsy-internal.json": `{"id":"4","subject":"visible","metadata":{"_internal":0},"status":"in_progress","description":5}`,
		"numeric/empty-subject.json":  `{"id":"5","subject":""}`,
		"numeric/broken.json":         `{`,
		"numeric/missing-id.json":     `{"subject":"missing id"}`,
		"numeric/not-task.json":       `[]`,
		"numeric/ignored.txt":         `{"id":"6","subject":"wrong extension"}`,
		"locale/z.json":               `{"id":"z","subject":"z"}`,
		"locale/a.json":               `{"id":"a","subject":"a","status":"in_progress"}`,
		"locale/A.json":               `{"id":"A","subject":"A"}`,
		"locale/accent.json":          `{"id":"á","subject":"accent"}`,
		"utf16/id.json":               `{"id":"\ud800","subject":"\udc00","description":"lone surrogate"}`,
		"coercion/object.json":        `{"id":{"legacy":true},"subject":"object"}`,
		"coercion/array.json":         `{"id":[1,null,"x"],"subject":"array","blocks":null}`,
		"coercion/true.json":          `{"id":true,"subject":"boolean"}`,
		"coercion/zero.json":          `{"id":0,"subject":"zero"}`,
		"empty/.revision":             `7`,
	}
	for name, value := range files {
		path := filepath.Join(config, "tasks", name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDesktopLegacyProjectionPreservesOriginalFields(t *testing.T) {
	root := t.TempDir()
	seedDesktopTasks(t, root)
	s := testStore(t, root)
	tasks, err := s.DesktopTasksForList("numeric")
	if err != nil || len(tasks) != 4 {
		t.Fatalf("legacy tasks: %s %v", raw(tasks), err)
	}
	want := []string{"1", "2", "4", "10"}
	for i, task := range tasks {
		if task.String("id") != want[i] {
			t.Fatalf("numeric order: %s", raw(tasks))
		}
	}
	if tasks[0].Get("activeForm") != nil || string(tasks[0].Get("description")) != `""` || string(tasks[0].Get("metadata")) != "null" || string(tasks[0].Get("subject")) != "42" {
		t.Fatalf("legacy optional/raw fields: %s", raw(tasks[0]))
	}
	if tasks[1].String("status") != "pending" || string(tasks[1].Get("blockedBy")) != `[1,"x",null]` || string(tasks[1].Get("blocks")) != `[]` || string(tasks[1].Get("activeForm")) != "false" {
		t.Fatalf("legacy arrays/status: %s", raw(tasks[1]))
	}
	lists, err := s.DesktopTaskLists()
	if err != nil || len(lists) != 4 {
		t.Fatalf("lists should exclude empty state directories: %s %v", raw(lists), err)
	}
	lone, err := s.DesktopTasksForList("utf16")
	if err != nil || len(lone) != 1 || string(lone[0].Get("id")) != `"\ud800"` || string(lone[0].Get("subject")) != `"\udc00"` {
		t.Fatalf("raw UTF-16 data changed: %s %v", raw(lone), err)
	}
}

func TestDesktopTasksMatchActualSourceFileReader(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	seedDesktopTasks(t, config)
	if err := os.Mkdir(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	names := []string{"numeric", "locale", "utf16", "coercion", "empty", "missing"}
	s := testStore(t, config)
	lists, err := s.DesktopTaskLists()
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := s.DesktopAllTasks()
	if err != nil {
		t.Fatal(err)
	}
	byList := map[string][]protocol.JSONObject{}
	for _, name := range names {
		byList[name], err = s.DesktopTasksForList(name)
		if err != nil {
			t.Fatal(err)
		}
	}
	actual := raw(map[string]any{"lists": lists, "tasks": tasks, "byList": byList})
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-desktop-tasks.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "run", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_DESKTOP_TASKS_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"lists": names}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	expected, err := cmd.Output()
	if err != nil || stderr.Len() > 0 {
		t.Fatalf("actual Source reader: %v %s", err, stderr.String())
	}
	var a, b any
	if err := json.Unmarshal(actual, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(expected, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Source desktop projection differs\nGo: %s\nSource: %s", actual, expected)
	}
}

func TestResetRetainsHiddenStateAndTaskIDs(t *testing.T) {
	s := testStore(t, t.TempDir())
	var updates atomic.Int32
	s.options.OnUpdate = func() { updates.Add(1) }
	if _, err := s.Create("reset", baseTask("old")); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{".hidden.json": `{"id":"99","subject":"hidden"}`, "10-malformed.json": `{`, "notes.txt": "keep"} {
		if err := os.WriteFile(filepath.Join(s.tasksDir("reset"), name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Reset("reset"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".hidden.json", "notes.txt"} {
		if _, err := os.Stat(filepath.Join(s.tasksDir("reset"), name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(s.tasksDir("reset"), "10-malformed.json")); !os.IsNotExist(err) {
		t.Fatalf("reset left malformed task file: %v", err)
	}
	if updates.Load() != 2 {
		t.Fatalf("original Task update notification was not delivered: %d", updates.Load())
	}
	created, err := s.Create("reset", baseTask("new"))
	if err != nil || created.TaskID != "11" || created.Revision != 3 {
		t.Fatalf("reset high-water/revision: %#v %v", created, err)
	}
	assertNoLeases(t, s.options.ConfigDir)
}

func TestResetAndIndependentTaskWritersNeverReuseIDs(t *testing.T) {
	root := t.TempDir()
	s := testStore(t, root)
	if _, err := s.Create("parallel-reset", baseTask("first")); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	start := make(chan struct{})
	ids, failures := make(chan string, 20), make(chan error, 40)
	for i := 0; i < 20; i++ {
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			created, err := testStore(t, root).Create("parallel-reset", baseTask("new"))
			if err == nil {
				ids <- created.TaskID
			}
			failures <- err
		}()
		go func() {
			defer workers.Done()
			<-start
			failures <- testStore(t, root).Reset("parallel-reset")
		}()
	}
	close(start)
	workers.Wait()
	close(ids)
	close(failures)
	for err := range failures {
		// Source's bounded 30-retry lease can reject requests under this
		// contention. Preserve that error instead of extending its retries;
		// successful operations still must never reuse a committed ID.
		if err != nil && !strings.Contains(err.Error(), "Lock file is already being held") {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] || id == "1" {
			t.Fatalf("concurrent reset reused task ID %s", id)
		}
		seen[id] = true
	}
	if len(seen) == 0 {
		t.Fatal("no independent TaskCreate completed")
	}
	for i := 2; i <= len(seen)+1; i++ {
		if !seen[strconv.Itoa(i)] {
			t.Fatalf("lost task creation %d: %#v", i, seen)
		}
	}
	created, err := s.Create("parallel-reset", baseTask("after restarts"))
	if err != nil || created.TaskID != strconv.Itoa(len(seen)+2) {
		t.Fatalf("reset broke the next independent writer: %#v %v", created, err)
	}
	assertNoLeases(t, root)
}
