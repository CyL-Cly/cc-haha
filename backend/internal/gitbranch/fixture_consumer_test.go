package gitbranch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestOriginalFixtureConsumerKeepsAwaitContinuationBeforeNativeNotification(t *testing.T) {
	var input struct {
		InitialHead, ChangedHead           string
		Calls                              int
		ExpectedValues                     []string
		ExpectedCWDReadsBeforeCallerReturn int
	}
	b, err := os.ReadFile("testdata/fixture_consumer_control.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &input); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, nil)
	h.write(t, ".git/HEAD", input.InitialHead)
	if value := h.branch(t); value != "original" {
		t.Fatal(value)
	}
	var cwdReads atomic.Int64
	var readsBeforeLastCallerReaction int64
	h.call(t, func(*cooperative.Scope) error {
		h.s.options.CWD = func() string { cwdReads.Add(1); return h.home }
		return nil
	})
	var operations []fixtureGitAwaitOperation
	var values []string
	for i := 0; i < input.Calls; i++ {
		index := i
		operations = append(operations, fixtureGitAwaitOperation{start: func(scope *cooperative.Scope) (*Future, error) {
			future, err := h.s.Branch(scope)
			if err != nil {
				return nil, err
			}
			if index == 0 {
				if err := os.WriteFile(filepath.Join(h.home, ".git/HEAD"), []byte(input.ChangedHead), 0600); err != nil {
					return nil, err
				}
				h.mu.Lock()
				notify := h.notify[filepath.Join(h.home, ".git/HEAD")]
				h.mu.Unlock()
				if notify == nil {
					return nil, fmt.Errorf("original native HEAD registration missing")
				}
				notify() // One actual admitted notification; no clock, sleep or phase oracle.
			}
			return future.Future, nil
		}, finish: func(value json.RawMessage) {
			values = append(values, stringValue(value))
			readsBeforeLastCallerReaction = cwdReads.Load()
		}})
	}
	if err := runFixtureGitAwaitChain(h.owner, operations); err != nil {
		t.Fatal(err)
	}
	// No task drain: capture the caller's own return before native work resumes.
	if !reflect.DeepEqual(values, input.ExpectedValues) {
		t.Fatalf("original await continuation displaced: values=%v want=%v", values, input.ExpectedValues)
	}
	if readsBeforeLastCallerReaction != int64(input.ExpectedCWDReadsBeforeCallerReturn) {
		t.Fatalf("native callback entered caller continuation: CWD reads=%d want=%d", readsBeforeLastCallerReaction, input.ExpectedCWDReadsBeforeCallerReturn)
	}
}

func TestOriginalFixtureDefaultOperationStillCallsBranch(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "work/.git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "work/.git/HEAD"), []byte("ref: refs/heads/original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input := fixtureInput{Home: home, Profile: originalProfile{CWD: "work", ClockStart: 1740000000000,
		Ops: []originalOp{{Kind: "original-default-branch"}}}}
	b, _ := json.Marshal(input)
	path := filepath.Join(home, "input.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CC_HAHA_GIT_BRANCH_FIXTURE", "1")
	t.Setenv("CC_HAHA_FIXTURE_TEMP_ROOT", filepath.Dir(home))
	t.Setenv("CC_HAHA_GIT_BRANCH_INPUT", path)
	TestGitBranchFixture(t)
	b, err = os.ReadFile(filepath.Join(home, "git-result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Values []observation }
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Values) != 2 || got.Values[1].Kind != "original-default-branch" || stringValue(got.Values[1].Value) != "original" {
		t.Fatalf("original default branch operation changed: %s", b)
	}
}
