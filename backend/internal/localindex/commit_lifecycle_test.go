package localindex

import (
	"context"
	"database/sql"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func contentCommitFixture(t *testing.T) (*Database, *ContentIndex, *ContentProjector, ContentCandidate, *sql.Conn) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "source.jsonl")
	if e := os.WriteFile(path, []byte(`{"type":"user","message":{"content":"physical commit proof"}}`+"\n"), 0600); e != nil {
		t.Fatal(e)
	}
	db, e := OpenDatabase(context.Background(), DatabaseOptions{Path: filepath.Join(home, "content.sqlite"), Kind: Content})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = db.Close() })
	index, _ := NewContentIndex(db, "scope", nil)
	projector, _ := NewContentProjector(ContentProjectorOptions{Index: index, Budget: sessionstore.NewBudget(), SpoolDir: home})
	probeDB, e := sql.Open("sqlite", db.path)
	if e != nil {
		t.Fatal(e)
	}
	probe, e := probeDB.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = probe.ExecContext(context.Background(), "PRAGMA busy_timeout=0"); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = probe.Close(); _ = probeDB.Close() })
	return db, index, projector, ContentCandidate{path, "project", "owner", path, 42}, probe
}
func assertCommitLeaseFree(t *testing.T, probe *sql.Conn) {
	t.Helper()
	if _, e := probe.ExecContext(context.Background(), "BEGIN IMMEDIATE"); e != nil {
		t.Fatalf("returned with write lease still held: %v", e)
	}
	_, _ = probe.ExecContext(context.Background(), "ROLLBACK")
}
func TestContentCommitStartedObserverIsClaimedAndThrownObserverRollsBack(t *testing.T) {
	_, index, p, candidate, probe := contentCommitFixture(t)
	seen := false
	p.o.OnCommitStarted = func() {
		seen = true
		if _, e := probe.ExecContext(context.Background(), "BEGIN IMMEDIATE"); e == nil {
			_, _ = probe.ExecContext(context.Background(), "ROLLBACK")
			t.Error("started observer ran before commit lease")
		}
		panic(errors.New("observer failed"))
	}
	result, e := p.Project(context.Background(), candidate)
	if !seen || e != nil || result.Kind != "retry" {
		t.Fatalf("observer failure was not retriable %+v %v", result, e)
	}
	assertCommitLeaseFree(t, probe)
	count, e := index.CountSources(context.Background())
	if e != nil || count != 0 {
		t.Fatalf("thrown observer landed source %d %v", count, e)
	}
	p.o.OnCommitStarted = nil
	if result, e := p.Project(context.Background(), candidate); e != nil || result.Kind != "indexed" {
		t.Fatalf("same database could not serve next commit %+v %v", result, e)
	}
}
func TestContentCommitCancelledAfterClaimDrainsAndCleansSpoolBeforeReturn(t *testing.T) {
	_, index, p, candidate, probe := contentCommitFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	p.o.OnCommitStarted = cancel
	_, e := p.Project(ctx, candidate)
	if !errors.Is(e, context.Canceled) {
		t.Fatalf("claimed cancel %v", e)
	}
	assertCommitLeaseFree(t, probe)
	count, e := index.CountSources(context.Background())
	if e != nil || count != 0 {
		t.Fatalf("cancel landed source %d %v", count, e)
	}
	spools, _ := filepath.Glob(filepath.Join(p.o.SpoolDir, "claude-search-spool-*"))
	if len(spools) != 0 {
		t.Fatal("cancel returned before spool drain", spools)
	}
	p.o.OnCommitStarted = nil
	if _, e = p.Project(context.Background(), candidate); e != nil {
		t.Fatal(e)
	}
}
func TestNativeQueuedCommitCancellationDoesNotWaitForOrAcquireWriterLease(t *testing.T) {
	db, _, _, _, probe := contentCommitFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	first := make(chan error, 1)
	go func() {
		first <- db.Transaction(context.Background(), func(op *Operation) error {
			close(entered)
			<-release
			return op.Exec("CREATE TABLE IF NOT EXISTS queued_cancel_proof(id TEXT)")
		})
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	called := atomic.Bool{}
	queued := make(chan error, 1)
	go func() { queued <- db.Transaction(ctx, func(*Operation) error { called.Store(true); return nil }) }()
	cancel()
	select {
	case e := <-queued:
		if !errors.Is(e, context.Canceled) || called.Load() {
			t.Fatal("queued claim executed", e)
		}
	case <-time.After(time.Second):
		t.Fatal("queued cancel waited on active commit")
	}
	once.Do(func() { close(release) })
	if e := <-first; e != nil {
		t.Fatal(e)
	}
	assertCommitLeaseFree(t, probe)
	if e := db.Transaction(context.Background(), func(op *Operation) error { return op.Exec("INSERT INTO queued_cancel_proof VALUES('after')") }); e != nil {
		t.Fatal(e)
	}
}
func TestSearchProjectionBudgetBoundsAllProjectorsAndCancelledWaiters(t *testing.T) {
	var budget projectionBudget
	active, e := budget.acquire(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() {
		release, e := budget.acquire(ctx)
		if release != nil {
			release()
		}
		queued <- e
	}()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		budget.mu.Lock()
		n := len(budget.waiting)
		budget.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if e = <-queued; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	done := make(chan error, 8)
	deadline = time.Now().Add(time.Second)
	for i := 0; i < 8; i++ {
		go func() {
			release, e := budget.acquire(context.Background())
			if release != nil {
				release()
			}
			done <- e
		}()
	}
	for time.Now().Before(deadline) {
		budget.mu.Lock()
		n := len(budget.waiting)
		budget.mu.Unlock()
		if n == 8 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, e = budget.acquire(context.Background()); errorCode(e, "") != "SEARCH_CONTENT_BUSY" {
		t.Fatalf("overflow %v", e)
	}
	active()
	for i := 0; i < 8; i++ {
		if e = <-done; e != nil {
			t.Fatal(e)
		}
	}
	again, e := budget.acquire(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	again()
}

func TestSearchProjectionBudgetSharedAcrossProjectorInstances(t *testing.T) {
	_, _, first, candidate1, _ := contentCommitFixture(t)
	_, _, second, candidate2, _ := contentCommitFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	first.o.OnBatch = func(int, int) { close(entered); <-release }
	firstDone := make(chan error, 1)
	go func() { _, e := first.Project(context.Background(), candidate1); firstDone <- e }()
	<-entered
	batches := atomic.Int32{}
	second.o.OnBatch = func(int, int) { batches.Add(1) }
	ctx, cancel := context.WithCancel(context.Background())
	pending := make(chan error, 1)
	go func() { _, e := second.Project(ctx, candidate2); pending <- e }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if e := <-pending; !errors.Is(e, context.Canceled) || batches.Load() != 0 {
		t.Fatal("different projector bypassed global budget", e, batches.Load())
	}
	once.Do(func() { close(release) })
	if e := <-firstDone; e != nil {
		t.Fatal(e)
	}
	if _, e := second.Project(context.Background(), candidate2); e != nil || batches.Load() != 1 {
		t.Fatal("cancelled waiter occupied slot", e)
	}
	// A deleted source is handled within its already acquired projection slot.
	if e := os.Remove(candidate2.Path); e != nil {
		t.Fatal(e)
	}
	done := make(chan ContentProjectResult, 1)
	go func() { r, _ := second.Project(context.Background(), candidate2); done <- r }()
	select {
	case result := <-done:
		if result.Kind != "deleted" {
			t.Fatal(result)
		}
	case <-time.After(time.Second):
		t.Fatal("deleted projection recursively acquired admission")
	}
}
