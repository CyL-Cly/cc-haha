package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestSchedulerDispatcherSameMinuteActiveManualAndStopDrain(t *testing.T) {
	o := disposableOptions(t)
	o.RandomBytes = func(n int) ([]byte, error) { b := make([]byte, n); _, e := rand.Read(b); return b, e }
	now := time.Date(2026, 10, 7, 1, 0, 0, 0, time.UTC)
	o.Now = func() time.Time { return now }
	var calls atomic.Int32
	started := make(chan struct{}, 10)
	released := make(chan struct{})
	var public atomic.Int32
	o.CreateSession = func(context.Context, string, string) (string, error) {
		public.Add(1)
		return "00000000-0000-4000-8000-000000000001", nil
	}
	o.Execute = func(ctx context.Context, _ ExecutionRequest, emit func(json.RawMessage)) (ExecutionResult, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-ctx.Done():
		case <-released:
		}
		emit([]byte(`{"type":"result","result":"actual completion"}`))
		return ExecutionResult{}, nil
	}
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	input := protocol.NewJSONObject(protocol.JSONField{Name: "cron", Value: quoted("* * * * *")}, protocol.JSONField{Name: "prompt", Value: quoted("automatic")}, protocol.JSONField{Name: "recurring", Value: []byte(`true`)})
	task, e := s.CreateTask(context.Background(), input)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("immediate first tick did not execute")
	}
	for i := 0; i < 5; i++ {
		if e = s.Tick(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	current, e := s.ExecuteTask(context.Background(), task, true)
	if e != nil || current.String("status") != "running" {
		t.Fatal("active same-task guard", current, e)
	}
	if calls.Load() != 1 || public.Load() != 0 {
		t.Fatal("automatic/active duplicate invoked public session", calls.Load(), public.Load())
	}
	close(released)
	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, _ := s.GetTaskRuns(context.Background(), task.String("id"))
		if len(rows) == 1 && rows[0].String("status") == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion not persisted", rows)
		}
		time.Sleep(time.Millisecond)
	}
	if e = s.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("minute key allowed duplicate")
	}
	if e = s.Stop(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = s.RunNow(context.Background(), task.String("id")); e == nil {
		t.Fatal("shutdown allowed detached new execution")
	}
	// Reopening keeps persisted/in-memory lastFired minute suppression.
	if e = s.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = s.Stop(context.Background()); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("restart duplicated same minute", calls.Load())
	}
}
func TestSchedulerCancellationJoinsActualExecutorAndRetainsCanonicalRun(t *testing.T) {
	o := disposableOptions(t)
	entered := make(chan struct{})
	finished := make(chan struct{})
	o.Execute = func(ctx context.Context, _ ExecutionRequest, _ func(json.RawMessage)) (ExecutionResult, error) {
		close(entered)
		<-ctx.Done()
		time.Sleep(10 * time.Millisecond)
		close(finished)
		return ExecutionResult{ExitCode: 143}, nil
	}
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	task, e := s.CreateTask(context.Background(), protocol.NewJSONObject(protocol.JSONField{Name: "cron", Value: quoted("* * * * *")}, protocol.JSONField{Name: "prompt", Value: quoted("cancel actual executor")}, protocol.JSONField{Name: "enabled", Value: []byte(`false`)}))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RunNow(context.Background(), task.String("id")); e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("manual run did not start")
	}
	if e = s.Stop(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case <-finished:
	default:
		t.Fatal("Stop returned before actual execution cleanup")
	}
	runs, e := s.GetTaskRuns(context.Background(), task.String("id"))
	if e != nil || len(runs) != 1 || runs[0].String("status") != "failed" {
		t.Fatal("cancellation lost failed run", runs, e)
	}
}
func TestSchedulerConcurrentRunsAtomicRetentionAndStaleRecovery(t *testing.T) {
	o := disposableOptions(t)
	var random atomic.Int32
	o.RandomBytes = func(n int) ([]byte, error) {
		b := make([]byte, n)
		v := random.Add(1)
		for i := range b {
			b[i] = byte(v)
		}
		return b, nil
	}
	now := time.Date(2026, 10, 7, 1, 0, 0, 0, time.UTC)
	o.Now = func() time.Time { return now }
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Stop(context.Background())
	var wg sync.WaitGroup
	for task := 0; task < 2; task++ {
		for n := 0; n < 110; n++ {
			wg.Add(1)
			go func(task, n int) {
				defer wg.Done()
				id := stringID(task)
				row := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: quoted(id + "-" + stringID(n))}, protocol.JSONField{Name: "taskId", Value: quoted(id)}, protocol.JSONField{Name: "startedAt", Value: quoted(iso(now))}, protocol.JSONField{Name: "status", Value: quoted("completed")})
				if e := s.store.mutateRuns(row, false); e != nil {
					t.Error(e)
				}
			}(task, n)
		}
	}
	wg.Wait()
	_, runs, _, e := s.store.read(s.store.runsPath(), "runs")
	if e != nil || len(runs) != 200 {
		t.Fatal("atomic trim lost multiple tasks", len(runs), e)
	}
	_ = os.WriteFile(s.store.runsPath(), []byte(`{"future":"protected","runs":[{"id":"stale","taskId":"a","startedAt":"2026-10-07T00:00:00Z","status":"running"},{"id":"active","taskId":"b","startedAt":"2026-10-07T00:59:30Z","status":"running"}]}`), 0600)
	if e = s.cleanupStaleRuns(); e != nil {
		t.Fatal(e)
	}
	file, rows, _, e := s.store.read(s.store.runsPath(), "runs")
	if e != nil || rows[0].String("status") != "failed" || rows[0].String("error") != "Process terminated before task could complete" || rows[1].String("status") != "running" || file.String("future") != "protected" {
		t.Fatal("stale lifecycle or unknown log root lost", file, rows, e)
	}
}
