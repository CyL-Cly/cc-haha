package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// The Source accessor intentionally proves only its warm original return graph.
// Native executes the real Host.completeCheckpoint against the real Writer and
// actual held native append. No replacement Flush, synthetic I/O or new Host
// observer is installed; checkpoint:settled is the existing lifecycle signal.
func TestActualCheckpointAwaitsOriginalSessionStorageFlushReturn(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{filepath.Join(home, ".claude"), filepath.Join(home, "tmp"), filepath.Join(home, "bin")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-session-storage-flush-return.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	sourceContext, sourceCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer sourceCancel()
	command := exec.CommandContext(sourceContext, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SESSION_STORAGE_FLUSH_RETURN=1", "NODE_ENV=production", "CLAUDE_CODE_SIMPLE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=" + filepath.Join(home, "bin")}
	body, _ := json.Marshal(map[string]string{"home": home})
	command.Stdin = bytes.NewReader(body)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("actual Source flush setup/consumer: %v stdout=%s stderr=%s", err, output, stderr.String())
	}
	var original struct {
		Observations                                              []bool
		Completed, SameExportPromise, SameDrainPromise, Persisted bool
		Blocked                                                   []string
		Scope                                                     string
	}
	if err := json.Unmarshal(output, &original); err != nil {
		t.Fatalf("Source raw %s: %v", output, err)
	}
	if stderr.Len() != 0 || !original.Completed || !original.SameExportPromise || !original.SameDrainPromise || !original.Persisted || len(original.Blocked) != 0 || len(original.Observations) != 2 || original.Observations[0] {
		t.Fatalf("Source original export was not a valid pending warm graph: %s stderr=%s", output, stderr.String())
	}

	files := newCleanupFiles()
	host, _, path := cleanupHost(t, files)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(files.release) }) }
	t.Cleanup(release)
	defer release()
	completed := false
	host.options.OnPhase = func(phase string) {
		files.event(phase)
		if phase == "checkpoint:settled" {
			completed = true
		}
	}
	cleanupCall(t, host.options.Owner, func(*cooperative.Scope) error {
		entry := cleanupObject(`{"type":"user","uuid":"held-append","message":{"role":"user","content":"actual flush export held native append"}}`)
		return host.options.Controller.Writer().Append(&entry)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	checkpointDone := make(chan error, 1)
	go func() {
		checkpointDone <- host.options.Owner.Run(ctx, host.completeCheckpoint)
	}()
	select {
	case <-files.entered:
	case <-ctx.Done():
		t.Fatal("actual checkpoint native append did not enter", context.Cause(ctx))
	}
	registered, observed := make(chan struct{}), make(chan struct{})
	var observations []bool
	receipt, err := host.options.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		// Begin returns the already active real D, not a new or substituted drain.
		drain, err := host.pending.Begin(scope)
		if err != nil {
			return err
		}
		select {
		case <-drain.Done():
			return errors.New("held actual native drain already completed")
		default:
		}
		// This administrative ready marker executes only inside Await after its
		// reaction has registered. Release never depends on elapsed time/tick count.
		if _, err = host.options.Owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { close(registered); return nil }); err != nil {
			return err
		}
		if err = drain.Await(scope); err != nil {
			return err
		}
		observations = append(observations, completed)
		_, err = host.options.Owner.Microtask(cooperative.Read, func(*cooperative.Scope) error {
			observations = append(observations, completed)
			close(observed)
			return nil
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-registered:
	case <-ctx.Done():
		t.Fatal("actual native drain observer not registered", context.Cause(ctx))
	}
	release()
	select {
	case <-receipt.Done():
		if err := receipt.Wait(); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("actual drain observer callback did not return", context.Cause(ctx))
	}
	select {
	case err := <-checkpointDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("actual Host checkpoint did not return", context.Cause(ctx))
	}
	select {
	case <-observed:
	case <-ctx.Done():
		t.Fatal("actual drain FIFO observation did not complete", context.Cause(ctx))
	}
	if !reflect.DeepEqual(observations, original.Observations) {
		t.Fatalf("actual Host checkpoint/export FIFO observations: native=%v original=%v SourceRaw=%s", observations, original.Observations, output)
	}
	disk, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(disk), `"uuid":"held-append"`) {
		t.Fatalf("actual native append absent: %v %s", err, disk)
	}
}
