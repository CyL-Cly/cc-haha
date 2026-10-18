package gitbranch

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type branchComputeReturnObservation struct {
	Phase              int
	Computed, Returned string
	Cached             bool
}

func TestActualSourceCachedBranchPublishesOnlyAfterComputeAsyncReturn(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned whole Source required")
	}
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-gitbranch-compute-return.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_BRANCH_COMPUTE_RETURN=1", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "PATH=" + home}
	command.Stdin = bytes.NewReader(raw(map[string]any{"home": home}))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, e := command.Output()
	if e != nil {
		t.Fatal("actual original compute-return", e, string(output), stderr.String())
	}
	var source struct {
		Events  []branchComputeReturnObservation
		Value   string
		Blocked []string
	}
	if e = json.Unmarshal(output, &source); e != nil || source.Value != "HEAD" || len(source.Blocked) != 0 {
		t.Fatal("original compute receipt", e, string(output))
	}
	t.Log("whole original Git watcher compute", string(output))
	h := newHarness(t, nil)
	var started *Future
	h.call(t, func(scope *cooperative.Scope) error { started = h.s.ensure(scope); return nil })
	if _, e := started.Wait(ctx); e != nil {
		t.Fatal(e)
	}
	var returned *BranchFuture
	actual := []branchComputeReturnObservation{}
	observed := make(chan struct{})
	var probe func(*cooperative.Scope, int)
	probe = func(scope *cooperative.Scope, phase int) {
		status := "pending"
		select {
		case <-returned.Done():
			status = "fulfilled"
		default:
		}
		actual = append(actual, branchComputeReturnObservation{Phase: phase, Returned: status, Cached: h.s.branch != nil})
		if phase < 2 {
			if _, e := h.owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error { probe(next, phase+1); return nil }); e != nil {
				t.Error(e)
			}
		} else {
			close(observed)
		}
	}
	h.call(t, func(scope *cooperative.Scope) error {
		h.s.observeBranchComputeReturn = func(scope *cooperative.Scope) {
			if _, e := h.owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error { probe(next, 0); return nil }); e != nil {
				t.Error(e)
			}
		}
		var e error
		returned, e = h.s.Branch(scope)
		return e
	})
	select {
	case <-observed:
	case <-ctx.Done():
		t.Fatal("native branch compute observer", ctx.Err())
	}
	want := append([]branchComputeReturnObservation(nil), source.Events...)
	for i := range want {
		if want[i].Computed != "fulfilled" {
			t.Fatal("actual Source compute Promise not fulfilled at its observer", source.Events)
		}
		// The old native code has no distinct compute Promise to observe.
		// Compare its actual returned Promise and business cache, not a
		// fabricated native status for that missing private Promise.
		want[i].Computed = ""
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatal("compute return and actual cache publication differ: Source", want, "Go", actual)
	}
	if value, e := returned.Wait(ctx); e != nil || value != source.Value {
		t.Fatal("actual native branch value", value, e)
	}
}
