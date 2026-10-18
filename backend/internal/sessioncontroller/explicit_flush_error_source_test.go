package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type explicitFlushErrorOutcome struct {
	Status string  `json:"status"`
	Code   *string `json:"code"`
}

func explicitFlushErrorResult(err error) explicitFlushErrorOutcome {
	if err == nil {
		return explicitFlushErrorOutcome{Status: "fulfilled"}
	}
	code := "UNEXPECTED"
	if errors.Is(err, syscall.EISDIR) {
		code = "EISDIR"
	}
	return explicitFlushErrorOutcome{Status: "rejected", Code: &code}
}

func TestActualSourceExplicitFlushDoesNotAdoptOtherExplicitDrainError(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source Bun required for explicit-flush ownership regression")
	}
	sourceHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-explicit-flush-error-ownership.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = sourceHome
	command.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + sourceHome, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_EXPLICIT_FLUSH_ERROR=1", "NODE_ENV=production", "CLAUDE_CODE_SIMPLE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=" + filepath.Join(sourceHome, "bin")}
	input, _ := json.Marshal(map[string]string{"home": sourceHome})
	command.Stdin = bytes.NewReader(input)
	var sourceError bytes.Buffer
	command.Stderr = &sourceError
	sourceRaw, err := command.Output()
	if err != nil {
		t.Fatalf("actual Source explicit-flush setup: %v raw=%s stderr=%s", err, sourceRaw, sourceError.String())
	}
	var source struct {
		Outcomes              []explicitFlushErrorOutcome `json:"outcomes"`
		Registration          []string                    `json:"registration"`
		SameFirstPromise      bool                        `json:"sameFirstPromise"`
		SameSecondPromise     bool                        `json:"sameSecondPromise"`
		DistinctReturns       bool                        `json:"distinctReturns"`
		ActiveAfterAdmissions bool                        `json:"activeAfterAdmissions"`
		Restored              bool                        `json:"restored"`
		FinalTracked          int                         `json:"finalTracked"`
		FinalQueued           int                         `json:"finalQueued"`
		FinalQueuePaths       int                         `json:"finalQueuePaths"`
		Scheduled             int                         `json:"scheduled"`
		Physical              bool                        `json:"physical"`
		Blocked               []string                    `json:"blocked"`
		SourceSHA256          string                      `json:"sourceSHA256"`
	}
	if err = json.Unmarshal(sourceRaw, &source); err != nil {
		t.Fatal("actual Source raw", err, string(sourceRaw))
	}
	code := "EISDIR"
	want := []explicitFlushErrorOutcome{{Status: "rejected", Code: &code}, {Status: "fulfilled"}}
	registration := []string{"first-original-explicit-flush", "second-original-explicit-flush"}
	if sourceError.Len() != 0 || !source.Physical || !source.SameFirstPromise || !source.SameSecondPromise || !source.DistinctReturns || source.ActiveAfterAdmissions || !source.Restored || source.FinalTracked != 0 || source.FinalQueued != 0 || source.FinalQueuePaths != 0 || source.Scheduled != 0 || len(source.Blocked) != 0 || !reflect.DeepEqual(source.Registration, registration) || !reflect.DeepEqual(source.Outcomes, want) || source.SourceSHA256 != "bc05983c8ed2164c437f3bf3cb11592a0447ca4ae94605bfa4bff0158f01dac6" {
		t.Fatalf("invalid actual Source explicit-flush ownership proof: %s stderr=%s", sourceRaw, sourceError.String())
	}
	t.Logf("actual original explicit-flush error ownership receipt %s", sourceRaw)

	owner, writer, pending := makePending(t, OSFiles{})
	path := writer.target()
	backup := path + ".explicit-flush-backup"
	var first, second *FlushFuture
	renamed := false
	restore := func() error {
		if !renamed {
			return nil
		}
		info, problem := os.Lstat(path)
		if problem == nil {
			if !info.IsDir() {
				return errors.New("private target changed before restore")
			}
			if problem = os.Remove(path); problem != nil {
				return problem
			}
		} else if !errors.Is(problem, os.ErrNotExist) {
			return problem
		}
		if problem = os.Rename(backup, path); problem != nil {
			return problem
		}
		renamed = false
		return nil
	}
	// On every exceptional path, join admitted original futures before restoring
	// the actual target and closing the writer. makePending's later cleanup closes
	// the Owner only after this physical join; no native gate or worker is left.
	defer func() {
		if first != nil {
			_ = first.Wait(context.Background())
		}
		if second != nil {
			_ = second.Wait(context.Background())
		}
		if problem := restore(); problem != nil {
			t.Error("deferred private target restore", problem)
		}
		if problem := pending.Close(context.Background()); problem != nil {
			t.Error("deferred physical writer join", problem)
		}
	}()
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		writer.mu.Lock()
		defer writer.mu.Unlock()
		queued := 0
		for _, entries := range writer.queues {
			queued += len(entries)
		}
		if queued == 0 || writer.stop == nil || pending.active != nil || pending.tracked != 0 {
			return errors.New("actual queued batch, uninvoked timer and no active drain required")
		}
		return nil
	})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refusing existing private backup", err)
	}
	if err = os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	renamed = true
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	activeAfterAdmissions := false
	// One legal synchronous Owner turn admits the exact two explicit Flush calls.
	// Worker native callbacks cannot execute on Owner between these admissions.
	err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		var problem error
		first, problem = pending.Flush(scope)
		if problem != nil {
			return problem
		}
		second, problem = pending.Flush(scope)
		if problem != nil {
			return problem
		}
		activeAfterAdmissions = pending.active != nil
		return nil
	})
	if err != nil {
		t.Fatal("two actual flush admissions", err)
	}
	firstObservedErr := first.Wait(ctx)
	secondObservedErr := second.Wait(ctx)
	// Even an expired observer deadline cannot transfer pathname ownership while
	// an admitted real worker remains. Join the actual immutable results first.
	firstErr := first.Wait(context.Background())
	secondErr := second.Wait(context.Background())
	// Observe both independent returns, then restore and physically join before
	// the business assertion. Both old and qualified-activefix must use this same
	// regression. EISDIR is the actual OS error, never a synthetic fixture fault.
	actual := []explicitFlushErrorOutcome{explicitFlushErrorResult(firstErr), explicitFlushErrorResult(secondErr)}
	if err = restore(); err != nil {
		t.Fatal("private target restore", err)
	}
	if err = pending.Close(ctx); err != nil {
		t.Fatal("physical writer close/join", err)
	}
	final, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(final, before) {
		t.Fatal("failed batch changed the restored original file", err, string(final))
	}
	if _, err = os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private backup remained after restore", err)
	}
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		writer.mu.Lock()
		defer writer.mu.Unlock()
		if pending.active != nil || pending.tracked != 0 || len(writer.queues) != 0 || writer.stop != nil {
			return errors.New("flush close left active work, queue or timer")
		}
		return nil
	})
	if ctx.Err() != nil {
		t.Fatal("explicit flush observer deadline expired after physical joins", ctx.Err(), firstObservedErr, secondObservedErr)
	}
	if firstObservedErr != firstErr || secondObservedErr != secondErr {
		t.Fatal("observer and physically joined errors changed identity", firstObservedErr, firstErr, secondObservedErr, secondErr)
	}
	if !errors.Is(firstErr, syscall.EISDIR) {
		t.Fatal("first actual native append open did not reject EISDIR", firstErr)
	}
	t.Logf("actual Go explicit-flush outcomes first=%v second=%v activeAfterAdmissions=%v distinctReturns=%v", firstErr, secondErr, activeAfterAdmissions, first != second)
	if !reflect.DeepEqual(actual, source.Outcomes) {
		t.Fatalf("second explicit flush adopted another explicit drain's native failure: Source=%+v Go=%+v SourceRaw=%s", source.Outcomes, actual, sourceRaw)
	}
}
