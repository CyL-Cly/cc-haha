package authhelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func waitCache(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("actual helper worker did not settle")
}
func TestCacheColdSingleflightSWRFailureClearEpochAndCloseJoin(t *testing.T) {
	var now atomic.Int64
	now.Store(1000)
	var calls atomic.Int32
	started := make(chan chan struct{}, 5)
	cache := New(Options{NowMillis: func() float64 { return float64(now.Load()) }})
	execute := func(ctx context.Context, _ string) (string, error) {
		n := calls.Add(1)
		gate := make(chan struct{})
		started <- gate
		select {
		case <-gate:
			if n == 2 {
				return "", errors.New("transient credential error")
			}
			if n == 1 {
				return "first", nil
			}
			return "new", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	r := Request{Command: "actual", TTL: "10", NonInteractive: true, Execute: execute}
	done := make(chan *string, 2)
	go func() { value, _ := cache.Get(context.Background(), r); done <- value }()
	gate := <-started
	go func() { value, _ := cache.Get(context.Background(), r); done <- value }()
	close(gate)
	if *<-done != "first" || *<-done != "first" || calls.Load() != 1 {
		t.Fatal("cold calls did not share actual helper invocation")
	}
	now.Store(1010)
	stale, err := cache.Get(context.Background(), r)
	if err != nil || *stale != "first" {
		t.Fatal("SWR did not immediately return stale key")
	}
	close(<-started)
	waitCache(t, func() bool { return cache.ElapsedMillis() == 0 && cache.Cached() != nil && *cache.Cached() == "first" })
	if calls.Load() != 2 {
		t.Fatal("stale refresh count")
	}
	cache.Clear()
	go func() { value, _ := cache.Get(context.Background(), r); done <- value }()
	orphan := <-started
	cache.Clear()
	go func() { value, _ := cache.Get(context.Background(), r); done <- value }()
	current := <-started
	close(current)
	if *<-done != "new" {
		t.Fatal("fresh epoch")
	}
	close(orphan)
	if *<-done != "new" {
		t.Fatal("orphan result")
	}
	if cache.Cached() == nil || *cache.Cached() != "new" {
		t.Fatal("orphan execution clobbered new cache")
	}
	cache.Clear()
	go func() { _, _ = cache.Get(context.Background(), r); done <- nil }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := cache.Close(ctx); err != nil {
		t.Fatal(err)
	}
	<-done
}
func TestHelperNativeProcessTrimFailureAndCancel(t *testing.T) {
	home := t.TempDir()
	options := ProcessOptions{CWD: home, Environment: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin"}}
	value, err := Execute(context.Background(), "printf '  owned-key \\n'", options)
	if err != nil || value != "owned-key" {
		t.Fatal(value, err)
	}
	for _, row := range []struct{ command, want string }{{"printf ' \\n'", "did not return a value"}, {"printf ' specific failure \\n' >&2; exit 7", "exited 7: specific failure"}} {
		_, err := Execute(context.Background(), row.command, options)
		if err == nil || err.Error() != row.want {
			t.Fatal(err, row.want)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = Execute(ctx, "printf ready > ready; sleep 20", options)
	if err == nil || err.Error() != "timed out" || time.Since(start) > time.Second {
		t.Fatal("real helper cancellation failed to join", err)
	}
	if raw, err := os.ReadFile(filepath.Join(home, "ready")); err != nil || string(raw) != "ready" {
		t.Fatal("physical helper never started")
	}
}
