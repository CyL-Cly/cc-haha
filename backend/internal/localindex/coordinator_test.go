package localindex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func newPrimaryFixture(t *testing.T) (*Coordinator, string, string) {
	t.Helper()
	home := disposableHome(t)
	scope := filepath.Join(home, ".claude")
	_ = os.MkdirAll(scope, 0700)
	service, e := NewCoordinator(CoordinatorOptions{Scope: scope, Budget: sessionstore.NewBudget(), Reduction: reductionOptions(), SourceMetadata: func(path string) (SourceMetadata, error) {
		_, e := os.Stat(path)
		return SourceMetadata{"2026-10-01T00:00:00.000Z", "2026-10-06T00:00:00.000Z"}, e
	}, Now: func() time.Time { return time.UnixMilli(42) }, WatcherOptions: WatchOptions{Debounce: time.Hour, MaxWait: time.Hour, SafetySweep: time.Hour}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = service.Stop(context.Background()) })
	return service, home, scope
}
func reducePrimary(v any, home string) any {
	value := decoded(v)
	var walk func(any) any
	walk = func(v any) any {
		switch value := v.(type) {
		case []any:
			out := []any{}
			for _, entry := range value {
				out = append(out, walk(entry))
			}
			return out
		case map[string]any:
			out := map[string]any{}
			for k, entry := range value {
				switch k {
				case "databaseBytes", "walBytes", "fingerprint", "mtimeMs", "fileIdentity", "createdAt", "modifiedAt", "modifiedAtMs":
					continue
				}
				out[k] = walk(entry)
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
func TestPrimaryCoordinatorActualSourceLifecycleAndAllReaders(t *testing.T) {
	line := `{"type":"user","uuid":"u1","timestamp":"2026-10-06T00:00:00Z","cwd":"/workspace","message":{"role":"user","content":"needle original task"}}` + "\n"
	assistant := `{"type":"assistant","uuid":"a1","timestamp":"2026-10-06T00:01:00Z","message":{"role":"assistant","content":"done","model":"deepseek-flash","usage":{"input_tokens":123,"output_tokens":7}}}` + "\n"
	ops := []map[string]any{{"method": "write", "path": "projects/p/s1.jsonl", "write": line + assistant}, {"method": "write", "path": "projects/p/s1/subagents/agent-worker.jsonl", "write": assistant}, {"method": "start"}, {"method": "readiness"}, {"method": "list"}, {"method": "get", "id": "s1"}, {"method": "find", "id": "s1"}, {"method": "metadata", "query": "needle"}, {"method": "locators", "path": "projects/p/s1.jsonl", "types": []string{"user"}}, {"method": "readEntries", "path": "projects/p/s1.jsonl", "project": "p", "types": []string{"assistant"}}, {"method": "stats"}, {"method": "title", "id": "s1", "title": "renamed"}, {"method": "get", "id": "s1"}, {"method": "notify", "path": "projects/p/s1.jsonl", "append": strings.TrimSuffix(line, "\n")}, {"method": "readiness"}, {"method": "notify", "path": "projects/p/s1.jsonl", "append": "\n"}, {"method": "list"}, {"method": "notify", "path": "projects/p/s1.jsonl", "remove": true}, {"method": "list"}, {"method": "sweep", "path": "projects/p/s1", "remove": true}, {"method": "readiness"}, {"method": "stop"}, {"method": "readiness"}, {"method": "start"}, {"method": "readiness"}, {"method": "rebuild"}, {"method": "readiness"}, {"method": "write", "path": "projects/p/new.jsonl", "write": line}, {"method": "sweep"}, {"method": "list"}, {"method": "sweep", "path": "projects", "remove": true}, {"method": "readiness"}}
	source := sourceIndex(t, disposableHome(t), map[string]any{"action": "primaryCoordinator", "operations": ops})
	var rows []json.RawMessage
	if e := json.Unmarshal(source["observations"], &rows); e != nil {
		t.Fatal(e)
	}
	c, home, scope := newPrimaryFixture(t)
	ctx := context.Background()
	for n, row := range ops {
		path := filepath.Join(scope, stringValue(raw(row["path"])))
		if text, ok := row["write"].(string); ok {
			_ = os.MkdirAll(filepath.Dir(path), 0700)
			_ = os.WriteFile(path, []byte(text), 0600)
		}
		if text, ok := row["append"].(string); ok {
			f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			_, _ = f.WriteString(text)
			_ = f.Close()
		}
		if row["remove"] == true {
			_ = os.RemoveAll(path)
		}
		method := row["method"].(string)
		var e error
		switch method {
		case "start":
			e = c.Start(ctx)
			if e == nil {
				e = c.WaitInitial(ctx)
			}
		case "stop":
			e = c.Stop(ctx)
		case "sweep":
			e = c.Reconcile(ctx, ReconciliationBatch{FullSweep: true})
		case "notify":
			e = c.Reconcile(ctx, ReconciliationBatch{Paths: []string{path}})
		case "rebuild":
			_, e = c.Rebuild(ctx)
			if e == nil {
				e = c.WaitInitial(ctx)
			}
		}
		if e != nil {
			t.Fatalf("case %d: %v", n, e)
		}
		var observed any = c.Status()
		switch method {
		case "readiness":
			observed = map[string]any{"sessions": c.SessionScopeReady(ctx), "activity": c.ActivityScopeReady(ctx), "failures": c.SourceScopedFailurePaths()}
		case "list":
			observed, e = c.List(ctx, SessionListOptions{})
		case "get":
			observed, e = c.Get(ctx, row["id"].(string))
		case "find":
			observed, e = c.FindFiles(ctx, row["id"].(string))
		case "metadata":
			observed, e = c.SearchMetadata(ctx, row["query"].(string), nil, nil)
		case "title":
			observed, e = c.UpdateTitle(ctx, row["id"].(string), row["title"].(string))
		case "locators", "readEntries":
			var types *[]string
			if array, ok := row["types"].([]string); ok {
				types = &array
			}
			if method == "locators" {
				observed, e = c.Locators(ctx, path, types)
			} else {
				observed, _, e = c.ReadEntries(ctx, path, row["project"].(string), types)
			}
		case "stats":
			observed, e = c.GetActivityStats(ctx, "all", time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC))
		}
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(reducePrimary(observed, home), decoded(rows[n])) {
			t.Fatalf("Source primary case %d %s\nNative=%s\nSource=%s", n, raw(row), raw(reducePrimary(observed, home)), rows[n])
		}
	}
}
func TestPrimaryStartsWithoutWaitingAndStopCancelsProjection(t *testing.T) {
	c, _, scope := newPrimaryFixture(t)
	path := filepath.Join(scope, "projects", "p", "s.jsonl")
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, []byte("{}\n"), 0600)
	entered := make(chan struct{})
	c.o.BeforeProject = func(ctx context.Context, _ SessionCandidate) error { close(entered); <-ctx.Done(); return ctx.Err() }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := c.Start(ctx); e != nil {
		t.Fatal(e)
	}
	<-entered
	start := time.Now()
	status := c.Status()
	if status.State != "building" || c.SessionScopeReady(ctx) {
		t.Fatalf("Unfinished projection falsely ready %+v", status)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("Health/status blocked behind transcript")
	}
	done := make(chan error, 1)
	go func() { done <- c.Stop(context.Background()) }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel owned projector")
	}
}
func TestPrimaryConcurrentLifecyclesAndSnapshotIsolation(t *testing.T) {
	c, _, _ := newPrimaryFixture(t)
	var workers sync.WaitGroup
	for n := 0; n < 12; n++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < 3; i++ {
				_ = c.Start(context.Background())
				_ = c.Status()
				_ = c.Stop(context.Background())
			}
		}()
	}
	workers.Wait()
	if e := c.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := c.WaitInitial(context.Background()); e != nil {
		t.Fatal(e)
	}
	if c.Status().State != "ready" {
		t.Fatal(c.Status())
	}
	copy := c.Status()
	if copy.LastUpdatedAt != nil {
		*copy.LastUpdatedAt = "mutated"
	}
	if c.Status().LastUpdatedAt != nil && *c.Status().LastUpdatedAt == "mutated" {
		t.Fatal("Public status aliases owner")
	}
}
func awaitPrimary(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Actual primary watcher did not settle")
}
func TestPrimaryActualNativeWatchAppendSubagentAndDirectoryReplacement(t *testing.T) {
	c, _, scope := newPrimaryFixture(t)
	c.o.WatcherOptions = WatchOptions{Debounce: 5 * time.Millisecond, MaxWait: 20 * time.Millisecond, SafetySweep: time.Hour, RetryBase: 5 * time.Millisecond}
	ctx := context.Background()
	if e := c.Start(ctx); e != nil {
		t.Fatal(e)
	}
	if e := c.WaitInitial(ctx); e != nil {
		t.Fatal(e)
	}
	project := filepath.Join(scope, "projects", "p")
	_ = os.MkdirAll(project, 0700)
	path := filepath.Join(project, "s.jsonl")
	line := `{"type":"user","timestamp":"2026-10-06T00:00:00Z","message":{"role":"user","content":"native first"}}` + "\n"
	_ = os.WriteFile(path, []byte(line), 0600)
	awaitPrimary(t, func() bool {
		row, _ := c.Get(ctx, "s")
		return row != nil && stringValue(row.Get("title")) == "native first"
	})
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = f.WriteString(line)
	_ = f.Close()
	awaitPrimary(t, func() bool { row, _ := c.Get(ctx, "s"); return row != nil && intValue(row.Get("messageCount")) == 2 })
	sub := filepath.Join(project, "s", "subagents", "workflows", "wf", "agent-real.jsonl")
	_ = os.MkdirAll(filepath.Dir(sub), 0700)
	_ = os.WriteFile(sub, []byte(line), 0600)
	awaitPrimary(t, func() bool {
		g := c.readGeneration()
		if g == nil {
			return false
		}
		count, e := g.activity.CountSources(ctx)
		return e == nil && count == 2 && c.ActivityScopeReady(ctx)
	})
	_ = os.RemoveAll(project)
	_ = os.MkdirAll(project, 0700)
	_ = os.WriteFile(path, []byte(strings.ReplaceAll(line, "native first", "directory replaced")), 0600)
	awaitPrimary(t, func() bool {
		row, _ := c.Get(ctx, "s")
		return row != nil && stringValue(row.Get("title")) == "directory replaced"
	})
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = f.WriteString(line)
	_ = f.Close()
	awaitPrimary(t, func() bool { row, _ := c.Get(ctx, "s"); return row != nil && intValue(row.Get("messageCount")) == 2 })
}
func TestPrimaryCorruptBackupFutureProtectionAndRuntimeConfiguration(t *testing.T) {
	ctx := context.Background()
	c, _, scope := newPrimaryFixture(t)
	path := filepath.Join(scope, "cc-haha", "db", filename(Primary))
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	original := []byte("actual damaged SQLite")
	_ = os.WriteFile(path, original, 0600)
	if e := c.Start(ctx); e != nil {
		t.Fatal(e)
	}
	if e := c.WaitInitial(ctx); e != nil {
		t.Fatal(e)
	}
	if c.Status().State != "ready" {
		t.Fatal(c.Status())
	}
	backups, e := os.ReadDir(filepath.Join(filepath.Dir(path), "backups"))
	if e != nil || len(backups) != 1 {
		t.Fatalf("Corruption not actually retained %v %v", backups, e)
	}
	retained, e := os.ReadFile(filepath.Join(filepath.Dir(path), "backups", backups[0].Name(), filepath.Base(path)))
	if e != nil || !bytes.Equal(retained, original) {
		t.Fatal("Original corrupt cache not backed up")
	}
	_ = c.Stop(ctx)
	db, e := OpenDatabase(ctx, DatabaseOptions{path, scope, Primary})
	if e != nil {
		t.Fatal(e)
	}
	e = db.Write(ctx, func(op *Operation) error { return op.Exec("PRAGMA user_version=99") })
	if e != nil {
		t.Fatal(e)
	}
	_ = db.Close()
	future, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Start(ctx); e == nil {
		t.Fatal("Future schema silently downgraded")
	}
	if c.Status().LastErrorCode == nil || *c.Status().LastErrorCode != "SCHEMA_UNSUPPORTED" {
		t.Fatal(c.Status())
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(future, after) {
		t.Fatal("Future index bytes mutated")
	}
	backups, _ = os.ReadDir(filepath.Join(filepath.Dir(path), "backups"))
	if len(backups) != 1 {
		t.Fatal("Future schema was backed up as corruption")
	}
	other := filepath.Join(t.TempDir(), "config")
	var mu sync.Mutex
	configuration := Configuration{Mode: "off", Scope: other}
	c.o.ResolveConfiguration = func() Configuration { mu.Lock(); defer mu.Unlock(); return configuration }
	if e = c.Start(ctx); e != nil {
		t.Fatal(e)
	}
	if c.Mode() != "off" {
		t.Fatal(c.Status())
	}
	mu.Lock()
	configuration.Mode = "on"
	mu.Unlock()
	if e = c.Start(ctx); e != nil {
		t.Fatal(e)
	}
	_ = c.WaitInitial(ctx)
	if c.Mode() != "on" || c.Status().State != "ready" {
		t.Fatal(c.Status())
	}
	mu.Lock()
	configuration.Mode = "shadow"
	mu.Unlock()
	_ = c.Status()
	awaitPrimary(t, func() bool { return c.Mode() == "shadow" && c.Status().State == "ready" })
	if c.ConsumerAvailable() {
		t.Fatal("Shadow index consumed as authoritative")
	}
}
func TestPrimarySourceLimitUsesBoundedOverlayAndRecoversOnRewrite(t *testing.T) {
	c, _, scope := newPrimaryFixture(t)
	project := filepath.Join(scope, "projects", "p")
	_ = os.MkdirAll(project, 0700)
	path := filepath.Join(project, "s.jsonl")
	good := `{"type":"user","message":{"content":"real task"}}` + "\n"
	_ = os.WriteFile(path, []byte(good), 0600)
	ctx := context.Background()
	_ = c.Start(ctx)
	_ = c.WaitInitial(ctx)
	file, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = file.WriteString(`{"type":"user","runtimeModelId":` + strconv.Quote(strings.Repeat("x", 4097)) + `}` + "\n")
	_ = file.Close()
	if e := c.Reconcile(ctx, ReconciliationBatch{Paths: []string{path}}); e != nil {
		t.Fatal(e)
	}
	status := c.Status()
	if status.State != "degraded" || status.LastErrorCode == nil || *status.LastErrorCode != SourceLimit {
		t.Fatal(status)
	}
	paths := c.SourceScopedFailurePaths()
	if !reflect.DeepEqual(paths, []string{path}) {
		t.Fatalf("Complete unaffected snapshot not offered for overlay %v", paths)
	}
	if c.ConsumerAvailable() {
		t.Fatal("Degraded snapshot used for search")
	}
	_ = os.WriteFile(path, []byte(good), 0600)
	if e := c.Reconcile(ctx, ReconciliationBatch{Paths: []string{path}}); e != nil {
		t.Fatal(e)
	}
	if c.Status().State != "ready" || !c.ConsumerAvailable() {
		t.Fatal(c.Status())
	}
}
func TestPrimaryOverlappingRebuildsShareOneActualBackup(t *testing.T) {
	c, _, _ := newPrimaryFixture(t)
	ctx := context.Background()
	if e := c.Start(ctx); e != nil {
		t.Fatal(e)
	}
	_ = c.WaitInitial(ctx)
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	c.o.Backup = func(o BackupOptions) (BackupResult, error) {
		calls++
		close(entered)
		<-release
		return BackupDatabaseFamily(o)
	}
	done := make(chan error, 2)
	go func() { _, e := c.Rebuild(ctx); done <- e }()
	<-entered
	go func() { _, e := c.Rebuild(ctx); done <- e }()
	time.Sleep(10 * time.Millisecond)
	close(release)
	for n := 0; n < 2; n++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	if calls != 1 {
		t.Fatalf("Concurrent rebuild moved cache %d times", calls)
	}
	if e := c.WaitInitial(ctx); e != nil {
		t.Fatal(e)
	}
	if c.Status().State != "ready" {
		t.Fatal(c.Status())
	}
}
