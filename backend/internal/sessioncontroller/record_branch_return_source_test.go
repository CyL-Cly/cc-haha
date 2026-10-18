package sessioncontroller

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
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
)

type recordBranchReturnObservation struct {
	Phase            int
	Cached, Returned string
}

func TestActualSourceRecordGetBranchPreservesAsyncAliasReturn(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned whole Source required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-transcript-record-branch-return.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_RECORD_BRANCH_RETURN=1", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "PATH=" + home}
	command.Stdin = bytes.NewReader(raw(map[string]any{"home": home}))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal("whole Source getBranch alias", err, string(output), stderr.String())
	}
	var source struct {
		Events        []recordBranchReturnObservation
		SamePromise   bool
		Value, Result string
		Blocked       []string
	}
	if err = json.Unmarshal(output, &source); err != nil || source.SamePromise || source.Value != "HEAD" || source.Result != source.Value || len(source.Blocked) != 0 {
		t.Fatal("actual original branch identity", err, string(output))
	}
	t.Log("whole original getBranch return", string(output))
	_, state, owner := boundController(t)
	service := actualOwnedGitService(t, state, owner)
	binding, err := NewOwnedGitRecordBranch(state, service)
	if err != nil {
		t.Fatal(err)
	}
	var returned RecordBranchFuture
	pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
		token, e := state.Context(scope)
		if e != nil {
			return e
		}
		returned, e = binding.Begin(scope, token)
		return e
	})
	if value, err := returned.Wait(ctx); err != nil || value != "HEAD" {
		t.Fatal("actual native warm branch", value, err)
	}
	observed := make(chan struct{})
	actual := []recordBranchReturnObservation{}
	var cached *gitbranch.BranchFuture
	var probe func(*cooperative.Scope, int)
	probe = func(scope *cooperative.Scope, phase int) {
		status := "pending"
		select {
		case <-returned.Done():
			status = "fulfilled"
		default:
		}
		cachedStatus := "pending"
		select {
		case <-cached.Done():
			cachedStatus = "fulfilled"
		default:
		}
		actual = append(actual, recordBranchReturnObservation{Phase: phase, Cached: cachedStatus, Returned: status})
		if phase < 2 {
			if _, err := owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error { probe(next, phase+1); return nil }); err != nil {
				t.Error(err)
			}
		} else {
			close(observed)
		}
	}
	pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
		binding.observeCachedReturn = func(value *gitbranch.BranchFuture) {
			cached = value
			if err := value.ThenOwner(scope, func(next *cooperative.Scope, _ string, failure error) {
				if failure != nil {
					t.Error(failure)
				}
				probe(next, 0)
			}, func(failure error) { t.Error(failure) }); err != nil {
				t.Error(err)
			}
		}
		token, e := state.Context(scope)
		if e != nil {
			return e
		}
		returned, e = binding.Begin(scope, token)
		return e
	})
	select {
	case <-observed:
	case <-ctx.Done():
		t.Fatal("actual branch return observer", ctx.Err())
	}
	if !reflect.DeepEqual(actual, source.Events) {
		t.Fatal("getBranch alias differs from actual getCachedBranch return: Source", source.Events, "Go", actual)
	}
	if value, err := returned.Wait(ctx); err != nil || value != source.Value {
		t.Fatal("actual branch result", value, err)
	}
}
