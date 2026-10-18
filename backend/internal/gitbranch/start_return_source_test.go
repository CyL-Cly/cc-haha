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

type branchStartIdentity struct{ FirstIsInner, SecondIsInner, FirstIsSecond bool }
type branchStartObservation struct {
	Phase                int
	Inner, First, Second string
}

func TestActualSourceGitEnsureStartedReturnsDistinctAdoptedPromises(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned whole Source required")
	}
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-gitbranch-start-return.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_BRANCH_START_RETURN=1", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "PATH=" + home}
	command.Stdin = bytes.NewReader(raw(map[string]string{"home": home}))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, e := command.Output()
	if e != nil {
		t.Fatal("actual original Git start", e, string(output), stderr.String())
	}
	var source struct {
		Identity    branchStartIdentity
		Events      []branchStartObservation
		Initialized bool
		Blocked     []string
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.Blocked) != 0 || !source.Initialized || len(source.Events) != 3 {
		t.Fatal("original Git start receipt", e, string(output))
	}
	t.Log("whole original ensureStarted", string(output))
	h := newHarness(t, nil)
	var first, second, inner *Future
	identity := branchStartIdentity{}
	actual := []branchStartObservation{}
	observed := make(chan struct{})
	status := func(f *Future) string {
		select {
		case <-f.Done():
			return "fulfilled"
		default:
			return "pending"
		}
	}
	var probe func(*cooperative.Scope, int)
	probe = func(scope *cooperative.Scope, phase int) {
		actual = append(actual, branchStartObservation{phase, status(inner), status(first), status(second)})
		if phase < 2 {
			if _, e := h.owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error { probe(next, phase+1); return nil }); e != nil {
				t.Error(e)
			}
		} else {
			close(observed)
		}
	}
	h.call(t, func(scope *cooperative.Scope) error {
		first = h.s.ensure(scope)
		inner = h.s.init
		second = h.s.ensure(scope)
		identity = branchStartIdentity{first == inner, second == inner, first == second}
		return inner.register(scope, reaction{run: func(next *cooperative.Scope, _ result) { probe(next, 0) }, rejected: func(e error) { t.Error(e) }})
	})
	if _, e := first.Wait(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := second.Wait(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-observed:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !reflect.DeepEqual(identity, source.Identity) || !reflect.DeepEqual(actual, source.Events) {
		t.Fatalf("actual ensureStarted async return differs: identity Go=%+v Source=%+v; reactions Go=%+v Source=%+v", identity, source.Identity, actual, source.Events)
	}
}
