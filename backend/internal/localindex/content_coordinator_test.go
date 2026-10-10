package localindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func newContentCoordinatorFixture(t *testing.T) (*ContentCoordinator, string, string) {
	t.Helper()
	home := disposableHome(t)
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	service, e := NewContentCoordinator(ContentCoordinatorOptions{Scope: config, Budget: sessionstore.NewBudget(), SpoolDir: filepath.Join(home, "tmp"), WatcherOptions: WatchOptions{Debounce: 5 * time.Millisecond, MaxWait: 20 * time.Millisecond, SafetySweep: time.Minute}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = service.Stop(context.Background()) })
	return service, home, config
}
func waitContentState(t *testing.T, c *ContentCoordinator, state string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c.Status().State == state {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Native content state did not settle: %+v wanted %s", c.Status(), state)
}
func reduceCoordinator(v any, home string) any {
	var value any
	_ = json.Unmarshal(raw(v), &value)
	var walk func(any) any
	walk = func(v any) any {
		switch value := v.(type) {
		case []any:
			out := []any{}
			for _, x := range value {
				out = append(out, walk(x))
			}
			return out
		case map[string]any:
			out := map[string]any{}
			for k, x := range value {
				switch k {
				case "databaseBytes", "walBytes", "sourceFingerprint", "sourceMtimeMs", "sourceFileIdentity", "modifiedAtMs":
					continue
				}
				out[k] = walk(x)
			}
			return out
		case string:
			return strings.ReplaceAll(value, home, "$HOME")
		default:
			return value
		}
	}
	return walk(value)
}
func TestContentCoordinatorActualSourceLifecycleAndCanonicalChanges(t *testing.T) {
	line := string(raw(map[string]any{"type": "user", "uuid": "u1", "message": map[string]any{"content": "needle searchable"}})) + "\n"
	sub := string(raw(map[string]any{"type": "assistant", "message": map[string]any{"content": "needle from worker"}})) + "\n"
	ops := []map[string]any{{"method": "write", "path": "projects/p/owner.jsonl", "write": line}, {"method": "write", "path": "projects/p/owner/subagents/worker.jsonl", "write": sub}, {"method": "start", "state": "ready"}, {"method": "query", "query": "needle"}, {"method": "suggest", "query": "needle"}, {"method": "notify", "path": "projects/p/owner.jsonl", "append": `{"type":"user","message":{"content":"pending needle"}}`}, {"method": "status", "state": "degraded"}, {"method": "query", "query": "needle"}, {"method": "notify", "path": "projects/p/owner.jsonl", "append": "\n"}, {"method": "status", "state": "ready"}, {"method": "query", "query": "needle", "options": map[string]any{"matchesPerSession": 1}}, {"method": "stop"}, {"method": "query", "query": "needle"}, {"method": "start", "state": "ready"}, {"method": "query", "query": "needle"}, {"method": "sweep", "path": "projects/p/owner", "remove": true}, {"method": "status", "state": "ready"}, {"method": "query", "query": "needle"}, {"method": "stop"}}
	source := sourceIndex(t, disposableHome(t), map[string]any{"action": "coordinator", "operations": ops})
	var rows []json.RawMessage
	if e := json.Unmarshal(source["observations"], &rows); e != nil {
		t.Fatal(e)
	}
	c, home, config := newContentCoordinatorFixture(t)
	// The Source comparator controls event delivery; native fsnotify has its own real-file tests.
	c.o.WatcherOptions.ListDirectories = func(context.Context, string) ([]string, error) { return []string{}, nil }
	ctx := context.Background()
	for n, row := range ops {
		path := filepath.Join(config, stringValue(raw(row["path"])))
		if text, ok := row["write"].(string); ok {
			_ = os.MkdirAll(filepath.Dir(path), 0700)
			_ = os.WriteFile(path, []byte(text), 0600)
			_ = os.Chtimes(path, time.Unix(1791244800, 0), time.Unix(1791244800, 0))
		}
		if text, ok := row["append"].(string); ok {
			file, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			_, _ = file.WriteString(text)
			_ = file.Close()
		}
		if row["remove"] == true {
			_ = os.RemoveAll(path)
		}
		method := row["method"].(string)
		switch method {
		case "start":
			if e := c.Start(ctx); e != nil {
				t.Fatal(e)
			}
		case "stop":
			if e := c.Stop(ctx); e != nil {
				t.Fatal(e)
			}
		case "notify":
			c.NotifyPath(path)
		case "sweep":
			c.NotifyFullSweep()
		}
		if state, ok := row["state"].(string); ok {
			waitContentState(t, c, state)
		}
		var actual any = c.Status()
		if method == "query" {
			var opts ContentQueryOptions
			_ = json.Unmarshal(raw(row["options"]), &opts)
			actual, _ = c.Query(ctx, row["query"].(string), opts)
		} else if method == "suggest" {
			actual, _ = c.Suggest(ctx, row["query"].(string), nil)
		}
		expected := decoded(rows[n])
		if !reflect.DeepEqual(reduceCoordinator(actual, home), expected) {
			t.Fatalf("Source coordinator observation %d %s\nNative=%s\nSource=%s", n, raw(row), raw(reduceCoordinator(actual, home)), rows[n])
		}
	}
}
func TestContentCoordinatorStartupDoesNotWaitForTranscriptAndStopCancels(t *testing.T) {
	c, _, config := newContentCoordinatorFixture(t)
	p := filepath.Join(config, "projects", "p", "session.jsonl")
	_ = os.MkdirAll(filepath.Dir(p), 0700)
	_ = os.WriteFile(p, []byte("{}\n"), 0600)
	entered := make(chan struct{})
	c.o.BeforeProject = func(ctx context.Context, _ ContentCandidate) error { close(entered); <-ctx.Done(); return ctx.Err() }
	if e := c.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Projection was not scheduled")
	}
	if s := c.Status(); s.State != "building" {
		t.Fatalf("Premature readiness: %+v", s)
	}
	if r, e := c.Query(context.Background(), "needle", ContentQueryOptions{}); e != nil || r != nil {
		t.Fatalf("Unbuilt index answered query: %v %+v", e, r)
	}
	done := make(chan error, 1)
	go func() { done <- c.Stop(context.Background()) }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel projection")
	}
	if c.Status().State != "building" {
		t.Fatal("Stopped coordinator did not reset")
	}
}
func TestContentCoordinatorNativeWatchAppendNewDirectoriesAndMissingRoot(t *testing.T) {
	c, _, config := newContentCoordinatorFixture(t)
	p := filepath.Join(config, "projects", "p", "session.jsonl")
	_ = os.MkdirAll(filepath.Dir(p), 0700)
	line := []byte(`{"type":"user","message":{"content":"original needle"}}` + "\n")
	_ = os.WriteFile(p, line, 0600)
	_ = c.Start(context.Background())
	waitContentState(t, c, "ready")
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = f.WriteString(`{"type":"user","message":{"content":"appended unique"}}` + "\n")
	_ = f.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r, _ := c.Query(context.Background(), "unique", ContentQueryOptions{})
		if r != nil && len(r.Sessions) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	r, _ := c.Query(context.Background(), "unique", ContentQueryOptions{})
	if r == nil || len(r.Sessions) != 1 {
		t.Fatal("Actual native fsnotify append was not reconciled")
	}
	newPath := filepath.Join(config, "projects", "newproject", "second.jsonl")
	_ = os.MkdirAll(filepath.Dir(newPath), 0700)
	_ = os.WriteFile(newPath, []byte(`{"type":"assistant","message":{"content":"new project unique"}}`+"\n"), 0600)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r, _ = c.Query(context.Background(), "unique", ContentQueryOptions{})
		if r != nil && len(r.Sessions) == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r == nil || len(r.Sessions) != 2 {
		t.Fatal("New project watcher was not attached")
	}
	_ = os.Rename(filepath.Join(config, "projects"), filepath.Join(config, "temporarily-unavailable"))
	c.NotifyFullSweep()
	waitContentState(t, c, "degraded")
	if r, _ = c.Query(context.Background(), "unique", ContentQueryOptions{}); r != nil {
		t.Fatal("Missing populated root was treated as authoritative empty")
	}
	if code := c.Status().LastErrorCode; code == nil || *code != "SEARCH_CONTENT_PROJECTS_ROOT_MISSING" {
		t.Fatalf("Missing root cause not preserved: %+v", c.Status())
	}
}
func TestContentCoordinatorStorageCorruptionAndUnsafeFamily(t *testing.T) {
	c, _, config := newContentCoordinatorFixture(t)
	c.o.StorageLimitBytes = 1
	_ = c.Start(context.Background())
	_ = c.WaitInitial(context.Background())
	if s := c.Status(); s.State != "degraded" || s.LastErrorCode == nil || *s.LastErrorCode != "SEARCH_CONTENT_STORAGE_LIMIT" {
		t.Fatalf("Storage limit ignored: %+v", s)
	}
	_ = c.Stop(context.Background())
	c.o.StorageLimitBytes = ContentStorageLimit
	p := filepath.Join(config, "cc-haha", "db", "search-index-v1.sqlite")
	_ = os.WriteFile(p, []byte("not a SQLite database"), 0600)
	_ = c.Start(context.Background())
	_ = c.WaitInitial(context.Background())
	if c.Status().State != "ready" {
		t.Fatalf("Disposable content corruption not recovered: %+v", c.Status())
	}
	_ = c.Stop(context.Background())
	outside := filepath.Join(filepath.Dir(config), "canonical-user-file")
	_ = os.WriteFile(outside, []byte("protected"), 0600)
	_ = os.Remove(p)
	_ = os.Symlink(outside, p)
	_ = c.Start(context.Background())
	s := c.Status()
	data, _ := os.ReadFile(outside)
	if s.State != "degraded" || s.LastErrorCode == nil || *s.LastErrorCode != "LOCAL_INDEX_UNSAFE_PATH" || !bytes.Equal(data, []byte("protected")) {
		t.Fatalf("Unsafe database family altered canonical state: %+v", s)
	}
}
func TestContentCoordinatorConcurrentLifecycleQueries(t *testing.T) {
	c, _, _ := newContentCoordinatorFixture(t)
	var group sync.WaitGroup
	for n := 0; n < 4; n++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for n := 0; n < 10; n++ {
				if e := c.Start(context.Background()); e != nil && !errors.Is(e, context.Canceled) {
					t.Error(e)
				}
				_, _ = c.Query(context.Background(), "needle", ContentQueryOptions{})
				_, _ = c.Suggest(context.Background(), "needle", nil)
				_ = c.Status()
				_ = c.Stop(context.Background())
			}
		}()
	}
	group.Wait()
}
