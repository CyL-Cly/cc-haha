package scheduler

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestSchedulerProjectionBusyCooldownAndUnsafePathKeepCanonicalBodies(t *testing.T) {
	o := disposableOptions(t)
	o.Environment = func() map[string]string { return map[string]string{"CC_HAHA_LOCAL_INDEX": "on"} }
	var millis atomic.Int64
	millis.Store(1791324000000)
	o.Now = func() time.Time { return time.UnixMilli(millis.Load()) }
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Stop(context.Background())
	write := func(id string) {
		t.Helper()
		b := []byte(`{"runs":[{"id":"` + id + `","taskId":"task","prompt":"protected private prompt","output":"protected private output","startedAt":"2026-10-07T02:00:00Z","status":"completed"}]}`)
		if e := os.WriteFile(s.store.runsPath(), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write("a")
	page, e := s.GetRunsPage(context.Background(), PageOptions{SummaryOnly: true})
	if e != nil || page.Revision == nil {
		t.Fatal("cold projection", page, e)
	}
	path := filepath.Join(o.ConfigDir, "cc-haha/db/scheduled-runs-v1.sqlite")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec("UPDATE scheduled_run_source SET updated_at_ms=1 WHERE singleton=1"); e != nil {
		t.Fatal(e)
	}
	write("b")
	page, e = s.GetRunsPage(context.Background(), PageOptions{SummaryOnly: true})
	if e != nil || page.Revision != nil || len(page.Runs) != 1 || page.Runs[0].String("id") != "b" {
		t.Fatal("busy projection overrode canonical read", page, e)
	}
	if e = tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	page, e = s.GetRunsPage(context.Background(), PageOptions{SummaryOnly: true})
	if e != nil || page.Revision != nil {
		t.Fatal("busy cooldown not retained", page, e)
	}
	millis.Add(5001)
	page, e = s.GetRunsPage(context.Background(), PageOptions{SummaryOnly: true})
	if e != nil || page.Revision == nil || len(page.Runs) != 1 || page.Runs[0].String("id") != "b" {
		t.Fatal("post-cooldown repair", page, e)
	}
	full, e := s.GetRunDetail(context.Background(), "b")
	if e != nil || full == nil || full.String("prompt") != "protected private prompt" || full.String("output") != "protected private output" {
		t.Fatal("projection failed canonical-body authority", full, e)
	}
}
func TestSchedulerProjectionFutureSchemaFallbackDoesNotRewriteFutureDB(t *testing.T) {
	o := disposableOptions(t)
	o.Environment = func() map[string]string { return map[string]string{"CC_HAHA_LOCAL_INDEX": "on"} }
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Stop(context.Background())
	path := filepath.Join(o.ConfigDir, "cc-haha/db/scheduled-runs-v1.sqlite")
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec("CREATE TABLE future_owner(body TEXT);INSERT INTO future_owner VALUES('must preserve');PRAGMA user_version=9")
	if e != nil {
		t.Fatal(e)
	}
	_ = db.Close()
	before, _ := os.ReadFile(path)
	b := []byte(`{"runs":[{"id":"a","taskId":"task","startedAt":"2026-10-07T02:00:00Z","status":"completed","output":"body"}]}`)
	_ = os.WriteFile(s.store.runsPath(), b, 0600)
	page, e := s.GetRunsPage(context.Background(), PageOptions{SummaryOnly: true})
	after, _ := os.ReadFile(path)
	if e != nil || len(page.Runs) != 1 || page.Revision != nil || string(before) != string(after) {
		t.Fatal("future projection destroyed canonical/future data", page, e)
	}
	// Canonical read remains tolerant of historical blank/BOM; genuinely corrupt
	// JSON is surfaced and protected rather than converted into an empty list.
	_ = os.WriteFile(s.store.runsPath(), []byte(`{bad protected`), 0600)
	if _, e = s.GetRunsPage(context.Background(), PageOptions{}); e == nil {
		t.Fatal("corrupt log silently returned ready empty")
	}
	if e = s.store.mutateRuns(page.Runs[0], false); e == nil {
		t.Fatal("corrupt log overwritten")
	}
	data, _ := os.ReadFile(s.store.runsPath())
	if string(data) != `{bad protected` {
		t.Fatal("protected data changed", string(data))
	}
}
func TestSchedulerCanonicalTaskWriteRetriesOnlyOriginalTransientErrors(t *testing.T) {
	for _, code := range []string{"ENOENT", "EPERM", "EACCES"} {
		t.Run(code, func(t *testing.T) {
			o := disposableOptions(t)
			files := &failRenameFiles{OSFiles: OSFiles{}, code: code}
			o.Files = files
			s, e := New(o)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Stop(context.Background())
			var input = objects([]map[string]any{{"cron": "* * * * *", "prompt": "actual write retry"}})[0]
			task, e := s.CreateTask(context.Background(), input)
			if code == "EACCES" {
				if e == nil || files.calls != 1 {
					t.Fatal("unexpected permission retry", task, e, files.calls)
				}
			} else if e != nil || files.calls != 2 {
				t.Fatal("transient error did not retry", task, e, files.calls)
			}
			entries, _ := os.ReadDir(o.ConfigDir)
			for _, entry := range entries {
				if entry.Name() != "scheduled_tasks.json" {
					t.Fatal("temporary file leaked", entry.Name())
				}
			}
		})
	}
}

type failRenameFiles struct {
	OSFiles
	code  string
	calls int
}

func (f *failRenameFiles) Rename(a, b string) error {
	f.calls++
	if f.calls == 1 {
		var e error
		switch f.code {
		case "ENOENT":
			e = os.ErrNotExist
		case "EPERM":
			e = syscall.EPERM
		case "EACCES":
			e = syscall.EACCES
		}
		return &os.PathError{Op: "rename", Path: b, Err: e}
	}
	return f.OSFiles.Rename(a, b)
}
