package localindex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestSearchProjectionAdmissionMatchesActualMainSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-search-projection-budget.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SEARCH_BUDGET_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]string{"homeDir": home}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var report struct {
		Result       json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &report); e != nil || len(report.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(output))
	}
	var budget projectionBudget
	active, e := budget.acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	events := []string{"first"}
	waiters := func(count int) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			budget.mu.Lock()
			n := len(budget.waiting)
			budget.mu.Unlock()
			if n == count {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("waiter not admitted", count)
	}
	cancelledCtx, stop := context.WithCancel(ctx)
	cancelled := make(chan error, 1)
	go func() {
		release, e := budget.acquire(cancelledCtx)
		if release != nil {
			release()
		}
		cancelled <- e
	}()
	waiters(1)
	stop()
	cancelledResult := "incorrect"
	if e = <-cancelled; e == context.Canceled {
		cancelledResult = "cancelled"
	}
	done := make(chan error, 8)
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		go func(n int) {
			release, e := budget.acquire(ctx)
			if e == nil {
				mu.Lock()
				events = append(events, "queued-"+strconv.Itoa(n))
				mu.Unlock()
				release()
			}
			done <- e
		}(i)
		waiters(i + 1)
	}
	_, e = budget.acquire(ctx)
	overflow := ""
	if e != nil {
		overflow = e.Error()
	}
	before := append([]string{}, events...)
	active()
	for i := 0; i < 8; i++ {
		if e = <-done; e != nil {
			t.Fatal(e)
		}
	}
	release, e := budget.acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	events = append(events, "after")
	release()
	var a, b any
	_ = json.Unmarshal(raw(map[string]any{"cancelled": cancelledResult, "overflow": overflow, "before": before, "events": events}), &a)
	_ = json.Unmarshal(report.Result, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Go%s Source%s", raw(a), report.Result)
	}
}
