package computeruse

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// This fixture puts a successful verifier stdout frame before the unchanged
// readiness deadline. Go race's artificial exit wait must not stand in for the
// external Swift verifier's native exit.
func TestNativeFixtureSuccessfulAttestationExitBeforeOriginalReadinessDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS AF_UNIX verifier boundary")
	}
	barrier := filepath.Join(t.TempDir(), "exit.fifo")
	if out, e := exec.Command("/usr/bin/mkfifo", barrier).CombinedOutput(); e != nil {
		t.Fatalf("mkfifo %v %s", e, out)
	}
	service, log := daemonFixture(t, map[string]string{"CC_HAHA_CU_VERIFIER_EXIT_BARRIER": barrier})
	original := service.options.Run
	var lock sync.Mutex
	var deadline time.Time
	service.options.Run = func(ctx context.Context, file string, args []string, extra []*os.File) (CommandResult, error) {
		if len(args) > 0 && args[0] == "attest_daemon_peer" {
			lock.Lock()
			deadline, _ = ctx.Deadline()
			lock.Unlock()
		}
		return original(ctx, file, args, extra)
	}
	done := make(chan error, 1)
	go func() {
		_, e := service.Call(context.Background(), "actual-exit-deadline", "screenshot", json.RawMessage(`{}`))
		done <- e
	}()
	limit := time.NewTimer(5 * time.Second)
	defer limit.Stop()
	observed := false
	for !observed {
		body, _ := os.ReadFile(log + ".phases")
		observed = strings.Contains(string(body), "verifier-before-exit")
		if observed {
			break
		}
		select {
		case <-limit.C:
			t.Fatal("actual verifier never emitted successful stdout before held exit")
		case <-time.After(time.Millisecond):
		}
	}
	lock.Lock()
	releaseAt := deadline.Add(-250 * time.Millisecond)
	lock.Unlock()
	timer := time.NewTimer(time.Until(releaseAt))
	defer timer.Stop()
	<-timer.C
	f, e := os.OpenFile(barrier, os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.Write([]byte{1})
	_ = f.Close()
	if e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatalf("successful physical peer frame released before unchanged deadline was rejected: %v", e)
		}
	case <-limit.C:
		t.Fatal("actual continuation did not join")
	}
}
