package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProjectHistoryCoalescesActualConcurrentReadersAcrossCallerCancellation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var loads atomic.Int32
	rows := []projectHistoryRow{historyTestRow(0, "/project"), historyTestRow(1, "/project")}
	h := newProjectSessionHistory(projectHistorySource{now: func() int64 { return 0 }, scope: func() string { return "scope" }, revision: func() string { return "revision" }, mutation: func() string { return "mutation" }, load: func(ctx context.Context) ([]projectHistoryRow, error) {
		if loads.Add(1) == 1 {
			close(entered)
		}
		<-release
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		return rows, nil
	}, hydrate: func(_ context.Context, row projectHistoryRow) (*sessionInfo, error) {
		info := row.info
		return &info, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, e := h.list(ctx, projectHistoryOptions{root: "/project"}); first <- e }()
	<-entered
	var wait sync.WaitGroup
	errors := make(chan error, 8)
	for n := 0; n < 8; n++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			page, e := h.list(context.Background(), projectHistoryOptions{root: "/project"})
			if e == nil && len(page.Sessions) != 2 {
				t.Error("Coalesced reader lost canonical rows")
			}
			errors <- e
		}()
	}
	cancel()
	close(release)
	wait.Wait()
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	if e := <-first; e != nil {
		t.Fatal(e)
	}
	if loads.Load() != 1 {
		t.Fatal("Metadata reads were not coalesced", loads.Load())
	}
}

func TestProjectHistoryLargeSnapshotRemainsCompleteWhileOlderSnapshotIsEvicted(t *testing.T) {
	rows := make([]projectHistoryRow, 50001)
	for n := range rows {
		rows[n] = historyTestRow(n%20, "/project")
		rows[n].info.ID = rows[n].info.ID + time.Duration(n).String()
	}
	h := newProjectSessionHistory(projectHistorySource{now: func() int64 { return 0 }, scope: func() string { return "scope" }, revision: func() string { return "revision" }, mutation: func() string { return "mutation" }, load: func(context.Context) ([]projectHistoryRow, error) { return rows, nil }, hydrate: func(_ context.Context, row projectHistoryRow) (*sessionInfo, error) {
		info := row.info
		return &info, nil
	}})
	first, e := h.list(context.Background(), projectHistoryOptions{root: "/project", limit: 1, hasLimit: true})
	if e != nil {
		t.Fatal(e)
	}
	latest, e := h.list(context.Background(), projectHistoryOptions{root: "/project", limit: 1, hasLimit: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.list(context.Background(), projectHistoryOptions{root: "/project", cursor: first.NextCursor}); e == nil {
		t.Fatal("Old snapshot survived row budget")
	}
	id, _, e := decodeProjectHistoryCursor(*latest.NextCursor)
	if e != nil {
		t.Fatal(e)
	}
	h.mu.Lock()
	count := len(h.snapshots[id].rows)
	h.mu.Unlock()
	if count != 50001 {
		t.Fatal("Large single project was truncated", count)
	}
	page, e := h.list(context.Background(), projectHistoryOptions{root: "/project", cursor: latest.NextCursor, limit: 1, hasLimit: true})
	if e != nil || len(page.Sessions) != 1 || page.NextCursor == nil {
		t.Fatal("Current large project is no longer browsable", e, page)
	}
}
